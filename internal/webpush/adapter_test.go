package webpush

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
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
	receiverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	receiverPublicKey := elliptic.Marshal(elliptic.P256(), receiverKey.PublicKey.X, receiverKey.PublicKey.Y)
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

	_, err := adapter.Deliver(context.Background(), delivery.Attempt{UserID: userID, Channel: Channel, Payload: []byte(`{}`)})
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

	_, firstErr := adapter.Deliver(context.Background(), delivery.Attempt{UserID: userID, Channel: Channel, Payload: []byte(`{}`)})
	_, secondErr := adapter.Deliver(context.Background(), delivery.Attempt{UserID: userID, Channel: Channel, Payload: []byte(`{}`)})
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

type fakeSender struct {
	result       SendResult
	err          error
	calls        int
	userID       uuid.UUID
	operationKey string
}

func (s *fakeSender) Send(_ context.Context, subscription push.Subscription, _ []byte, operationKey string) (SendResult, error) {
	s.calls++
	s.userID = subscription.UserID
	s.operationKey = operationKey
	return s.result, s.err
}
