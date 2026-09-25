package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/push"
)

func TestPushSubscriptionRegistrationRequiresAuthenticatedUser(t *testing.T) {
	store := &fakePushSubscriptionStore{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/push-subscriptions", bytes.NewBufferString(`{"endpoint":"https://push.example.test/send","keys":{"p256dh":"BAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","auth":"AAAAAAAAAAAAAAAAAAAAAA"}}`))

	pushSubscriptionCreateHandler(store).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestPushSubscriptionRegistrationDoesNotReturnProtocolSecrets(t *testing.T) {
	userID := uuid.New()
	store := &fakePushSubscriptionStore{subscribed: push.Subscription{ID: uuid.New(), UserID: userID, Endpoint: "https://push.example.test/send", P256DH: "secret-p256dh", Auth: "secret-auth", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/push-subscriptions", bytes.NewBufferString(`{"endpoint":"https://push.example.test/send","keys":{"p256dh":"BAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","auth":"AAAAAAAAAAAAAAAAAAAAAA"}}`))
	request = request.WithContext(auth.WithPrincipal(context.Background(), auth.Principal{UserID: userID}))

	pushSubscriptionCreateHandler(store).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("secret-p256dh")) || bytes.Contains(recorder.Body.Bytes(), []byte("secret-auth")) || bytes.Contains(recorder.Body.Bytes(), []byte("https://push.example.test")) {
		t.Fatalf("response exposed subscription secret: %s", recorder.Body.String())
	}
}

func TestPushSubscriptionDeleteCannotCrossUserBoundary(t *testing.T) {
	ownerID := uuid.New()
	otherID := uuid.New()
	subscriptionID := uuid.New()
	store := &fakePushSubscriptionStore{subscribed: push.Subscription{ID: subscriptionID, UserID: ownerID}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/push-subscriptions/"+subscriptionID.String(), nil)
	request = request.WithContext(auth.WithPrincipal(context.Background(), auth.Principal{UserID: otherID}))

	pushSubscriptionDeleteHandler(store).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound || store.deleted {
		t.Fatalf("status/deleted = %d/%v, want 404/false", recorder.Code, store.deleted)
	}
}

type fakePushSubscriptionStore struct {
	subscribed push.Subscription
	deleted    bool
}

func (s *fakePushSubscriptionStore) Upsert(_ context.Context, _ uuid.UUID, _ push.SubscriptionInput) (push.Subscription, bool, error) {
	return s.subscribed, true, nil
}

func (s *fakePushSubscriptionStore) Delete(_ context.Context, userID, subscriptionID uuid.UUID) error {
	if userID != s.subscribed.UserID || subscriptionID != s.subscribed.ID {
		return push.ErrSubscriptionNotFound
	}
	s.deleted = true
	return nil
}

var _ push.SubscriptionStore = (*fakePushSubscriptionStore)(nil)

func (s *fakePushSubscriptionStore) ListForDelivery(context.Context, uuid.UUID) ([]push.Subscription, error) {
	return nil, nil
}

func (s *fakePushSubscriptionStore) RemoveExpired(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
