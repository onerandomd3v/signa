package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	webpushlib "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/delivery"
	"github.com/onerandomd3v/signa/internal/push"
)

func TestLibrarySenderDeliversEncryptedRequestAndCorrelatesOperation(t *testing.T) {
	privateKey, publicKey, err := webpushlib.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiverKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	receiverPublicKey := receiverKey.PublicKey().Bytes()
	httpClient := &recordingHTTPClient{}
	sender := &librarySender{config: Config{Subscriber: "mailto:alerts@example.test", VAPIDPublicKey: publicKey, VAPIDPrivateKey: privateKey, TTL: 60, Timeout: time.Second}, client: httpClient}

	result, err := sender.Send(context.Background(), push.Subscription{Endpoint: "https://push.example.test/send", P256DH: base64.RawURLEncoding.EncodeToString(receiverPublicKey), Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}, []byte(`{"message":"safe"}`), "signa:delivery:stable-key")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if result.StatusCode != http.StatusCreated || httpClient.request == nil {
		t.Fatalf("result/request = %+v/%v", result, httpClient.request)
	}
	if got := httpClient.request.Header.Get("Topic"); got != operationTopic("signa:delivery:stable-key") {
		t.Fatalf("Topic = %q, want stable operation correlation", got)
	}
	if httpClient.request.Header.Get("Content-Encoding") != "aes128gcm" || httpClient.body == "{\"message\":\"safe\"}" {
		t.Fatalf("provider request was not encrypted web push payload")
	}
}

func TestOperationTopicIsStableBoundedAndURLSafe(t *testing.T) {
	first := operationTopic("signa:delivery:stable-key")
	if first != operationTopic("signa:delivery:stable-key") {
		t.Fatal("operation topic is not stable")
	}
	if len(first) > 32 {
		t.Fatalf("operation topic length = %d, want <= 32", len(first))
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(first) {
		t.Fatalf("operation topic = %q, want unpadded URL-safe base64", first)
	}
	if first == operationTopic("signa:delivery:other-key") {
		t.Fatal("different operation keys produced the same topic")
	}
}

func TestNewConfiguredAdapterRejectsMalformedVAPIDConfiguration(t *testing.T) {
	_, err := NewConfiguredAdapter(nil, Config{Subscriber: "mailto:alerts@example.test", VAPIDPublicKey: "public-key", VAPIDPrivateKey: "private-key", TTL: 60, Timeout: time.Second}, nil)
	if err == nil || !errors.Is(err, ErrInvalidProviderConfig) {
		t.Fatalf("NewConfiguredAdapter() error = %v, want invalid provider config", err)
	}
}

type recordingHTTPClient struct {
	request *http.Request
	body    string
}

func (c *recordingHTTPClient) Do(request *http.Request) (*http.Response, error) {
	c.request = request
	body, _ := io.ReadAll(request.Body)
	c.body = string(body)
	return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted")), Request: request}, nil
}

func TestAdapterDeliversOnlyToAttemptUserAndPreservesOperationKey(t *testing.T) {
	userID := uuid.New()
	store := &fakeSubscriptionStore{subscriptions: []push.Subscription{{ID: uuid.New(), UserID: userID, Endpoint: "https://push.example.test/one", P256DH: "key", Auth: "auth"}}}
	sender := &fakeSender{result: SendResult{StatusCode: 201, Response: "accepted"}}
	adapter := NewAdapter(store, sender, Config{})

	result, err := adapter.Deliver(context.Background(), delivery.Attempt{
		DeliveryID:   uuid.New().String(),
		UserID:       userID,
		Channel:      Channel,
		Payload:      []byte(`{"message":"safe"}`),
		OperationKey: "signa:delivery:stable-key",
	})
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if result.Response != "accepted" || store.listedUser != userID {
		t.Fatalf("result/store user = %+v/%s", result, store.listedUser)
	}
	if sender.userID != userID || sender.operationKey != "signa:delivery:stable-key" {
		t.Fatalf("sender target = %s, operation key = %q", sender.userID, sender.operationKey)
	}
}

func TestAdapterClassifiesProviderNetworkFailureAsTransient(t *testing.T) {
	userID := uuid.New()
	store := &fakeSubscriptionStore{subscriptions: []push.Subscription{{ID: uuid.New(), UserID: userID}}}
	errProviderTimeout := errors.New("provider timeout")
	adapter := NewAdapter(store, &fakeSender{err: errProviderTimeout}, Config{})

	_, err := adapter.Deliver(context.Background(), delivery.Attempt{DeliveryID: uuid.New().String(), UserID: userID, Channel: Channel, Payload: []byte(`{}`)})
	if err == nil || !errors.Is(err, errProviderTimeout) {
		t.Fatalf("Deliver() error = %v, want wrapped provider error", err)
	}
	var classified *delivery.DeliveryError
	if !errors.As(err, &classified) || classified.Kind != delivery.FailureTransient {
		t.Fatalf("classified error = %T/%v, want transient", err, err)
	}
}

func TestAdapterRemovesExpiredSubscriptionAndDoesNotRetryItEndlessly(t *testing.T) {
	userID := uuid.New()
	subscription := push.Subscription{ID: uuid.New(), UserID: userID, Endpoint: "https://push.example.test/expired"}
	store := &fakeSubscriptionStore{subscriptions: []push.Subscription{subscription}}
	sender := &fakeSender{result: SendResult{StatusCode: 410, Response: "gone"}}
	adapter := NewAdapter(store, sender, Config{})
	deliveryID := uuid.New().String()

	_, firstErr := adapter.Deliver(context.Background(), delivery.Attempt{DeliveryID: deliveryID, UserID: userID, Channel: Channel, Payload: []byte(`{}`)})
	_, secondErr := adapter.Deliver(context.Background(), delivery.Attempt{DeliveryID: deliveryID, UserID: userID, Channel: Channel, Payload: []byte(`{}`)})
	if firstErr == nil || secondErr == nil {
		t.Fatalf("expired errors = %v/%v, want permanent errors", firstErr, secondErr)
	}
	if store.deleted != 1 || len(store.subscriptions) != 0 {
		t.Fatalf("expired cleanup = deleted %d, remaining %d", store.deleted, len(store.subscriptions))
	}
	var classified *delivery.DeliveryError
	if !errors.As(firstErr, &classified) || classified.Kind != delivery.FailurePermanent {
		t.Fatalf("first error = %v, want permanent", firstErr)
	}
}

func TestAdapterRetriesOnlyUnresolvedSubscriptions(t *testing.T) {
	userID := uuid.New()
	first := push.Subscription{ID: uuid.New(), UserID: userID, Endpoint: "https://push.example.test/one"}
	second := push.Subscription{ID: uuid.New(), UserID: userID, Endpoint: "https://push.example.test/two"}
	store := &fakeSubscriptionStore{subscriptions: []push.Subscription{first, second}}
	sender := &fakeSender{results: map[uuid.UUID][]SendResult{
		first.ID:  {{StatusCode: http.StatusCreated, Response: "first accepted"}},
		second.ID: {{StatusCode: http.StatusServiceUnavailable, Response: "retry"}, {StatusCode: http.StatusCreated, Response: "second accepted"}},
	}}
	adapter := NewAdapter(store, sender, Config{})
	attempt := delivery.Attempt{DeliveryID: uuid.New().String(), UserID: userID, Channel: Channel, Payload: []byte(`{}`)}

	if _, err := adapter.Deliver(context.Background(), attempt); err == nil {
		t.Fatal("first delivery error = nil, want transient error")
	}
	if _, err := adapter.Deliver(context.Background(), attempt); err != nil {
		t.Fatalf("second delivery error = %v, want success", err)
	}
	if sender.callsBySubscription[first.ID] != 1 || sender.callsBySubscription[second.ID] != 2 {
		t.Fatalf("calls by subscription = %+v, want first=1 second=2", sender.callsBySubscription)
	}
	if store.results[first.ID].State != push.DeliveryResultSucceeded || store.results[second.ID].State != push.DeliveryResultSucceeded {
		t.Fatalf("durable results = %+v, want both succeeded", store.results)
	}
}

func TestAdapterDoesNotDeleteSubscriptionsForNonExpiredClientErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			userID := uuid.New()
			subscription := push.Subscription{ID: uuid.New(), UserID: userID, Endpoint: "https://push.example.test/rejected"}
			store := &fakeSubscriptionStore{subscriptions: []push.Subscription{subscription}}
			adapter := NewAdapter(store, &fakeSender{result: SendResult{StatusCode: status}}, Config{})

			_, err := adapter.Deliver(context.Background(), delivery.Attempt{DeliveryID: uuid.New().String(), UserID: userID, Channel: Channel, Payload: []byte(`{}`)})
			if err == nil {
				t.Fatal("Deliver() error = nil, want permanent error")
			}
			if store.deleted != 0 || len(store.subscriptions) != 1 {
				t.Fatalf("subscription cleanup = deleted %d, remaining %d; want no deletion", store.deleted, len(store.subscriptions))
			}
			if store.results[subscription.ID].State != push.DeliveryResultPermanent {
				t.Fatalf("result state = %q, want permanent", store.results[subscription.ID].State)
			}
		})
	}
}

func TestAdapterRejectsNonWebPushChannelWithoutSending(t *testing.T) {
	userID := uuid.New()
	sender := &fakeSender{}
	adapter := NewAdapter(&fakeSubscriptionStore{subscriptions: []push.Subscription{{UserID: userID}}}, sender, Config{})

	_, err := adapter.Deliver(context.Background(), delivery.Attempt{UserID: userID, Channel: "EMAIL", Payload: []byte(`{}`)})
	if err == nil || sender.calls != 0 {
		t.Fatalf("Deliver() error/calls = %v/%d, want error and no send", err, sender.calls)
	}
}

type fakeSubscriptionStore struct {
	subscriptions []push.Subscription
	listedUser    uuid.UUID
	deleted       int
	results       map[uuid.UUID]push.DeliveryResult
	claimed       map[uuid.UUID]bool
}

func (s *fakeSubscriptionStore) ListForDelivery(_ context.Context, userID uuid.UUID) ([]push.Subscription, error) {
	s.listedUser = userID
	result := make([]push.Subscription, 0, len(s.subscriptions))
	for _, item := range s.subscriptions {
		if item.UserID == userID {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *fakeSubscriptionStore) RemoveExpired(_ context.Context, userID, subscriptionID uuid.UUID) error {
	s.deleted++
	for index, item := range s.subscriptions {
		if item.UserID == userID && item.ID == subscriptionID {
			s.subscriptions = append(s.subscriptions[:index], s.subscriptions[index+1:]...)
			break
		}
	}
	return nil
}

func (s *fakeSubscriptionStore) EnsureDeliveryResults(_ context.Context, deliveryID, _ uuid.UUID, subscriptions []push.Subscription) error {
	if s.results == nil {
		s.results = make(map[uuid.UUID]push.DeliveryResult)
	}
	for _, subscription := range subscriptions {
		if _, ok := s.results[subscription.ID]; !ok {
			s.results[subscription.ID] = push.DeliveryResult{DeliveryID: deliveryID, SubscriptionID: subscription.ID, State: push.DeliveryResultPending}
		}
	}
	return nil
}

func (s *fakeSubscriptionStore) LoadDeliveryResults(_ context.Context, deliveryID uuid.UUID) (map[uuid.UUID]push.DeliveryResult, error) {
	result := make(map[uuid.UUID]push.DeliveryResult)
	for id, item := range s.results {
		if item.DeliveryID == deliveryID {
			result[id] = item
		}
	}
	return result, nil
}

func (s *fakeSubscriptionStore) ClaimDeliveryResult(_ context.Context, deliveryID, subscriptionID uuid.UUID, _ time.Duration) (bool, error) {
	if s.claimed == nil {
		s.claimed = make(map[uuid.UUID]bool)
	}
	if s.claimed[subscriptionID] {
		return false, nil
	}
	item := s.results[subscriptionID]
	if item.DeliveryID != deliveryID || (item.State != push.DeliveryResultPending && item.State != push.DeliveryResultRetryable) {
		return false, nil
	}
	s.claimed[subscriptionID] = true
	return true, nil
}

func (s *fakeSubscriptionStore) CompleteDeliveryResult(_ context.Context, deliveryID, subscriptionID uuid.UUID, state push.DeliveryResultState, response, message string) error {
	item := s.results[subscriptionID]
	item.DeliveryID = deliveryID
	item.SubscriptionID = subscriptionID
	item.State = state
	item.ProviderResponse = response
	item.ErrorMessage = message
	s.results[subscriptionID] = item
	if s.claimed != nil {
		delete(s.claimed, subscriptionID)
	}
	return nil
}

func (s *fakeSubscriptionStore) CompleteExpiredDeliveryResult(ctx context.Context, deliveryID, userID, subscriptionID uuid.UUID, response, message string) error {
	if err := s.CompleteDeliveryResult(ctx, deliveryID, subscriptionID, push.DeliveryResultExpired, response, message); err != nil {
		return err
	}
	return s.RemoveExpired(ctx, userID, subscriptionID)
}

type fakeSender struct {
	result              SendResult
	err                 error
	calls               int
	userID              uuid.UUID
	operationKey        string
	results             map[uuid.UUID][]SendResult
	callsBySubscription map[uuid.UUID]int
}

func (s *fakeSender) Send(_ context.Context, subscription push.Subscription, _ []byte, operationKey string) (SendResult, error) {
	s.calls++
	s.userID = subscription.UserID
	s.operationKey = operationKey
	if s.callsBySubscription == nil {
		s.callsBySubscription = make(map[uuid.UUID]int)
	}
	s.callsBySubscription[subscription.ID]++
	if results := s.results[subscription.ID]; len(results) > 0 {
		result := results[0]
		s.results[subscription.ID] = results[1:]
		return result, nil
	}
	return s.result, s.err
}
