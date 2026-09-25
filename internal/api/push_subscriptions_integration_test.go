//go:build integration

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/push"
)

func TestPushSubscriptionRoutesUseCanonicalSessionAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, databaseName := createMediaIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runMediaGoose(t, ctx, testURL, "up")

	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	userA, userB := uuid.New(), uuid.New()
	sessionStore := auth.NewPostgresStore(pool)
	principalSession, err := sessionStore.Create(ctx, userA, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, pool, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{Store: sessionStore})

	t.Run("missing session returns canonical JSON 401", func(t *testing.T) {
		response := servePushRequest(handler, http.MethodPost, "/push-subscriptions", "", validPushSubscriptionJSON("missing"))
		assertCanonicalAuthError(t, response, http.StatusUnauthorized, "unauthenticated")
	})

	t.Run("invalid, expired, and revoked sessions return canonical JSON 401", func(t *testing.T) {
		invalid := servePushRequest(handler, http.MethodPost, "/push-subscriptions", "invalid-session", validPushSubscriptionJSON("invalid"))
		assertCanonicalAuthError(t, invalid, http.StatusUnauthorized, "unauthenticated")

		expired, err := sessionStore.Create(ctx, userA, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		decodedSecret, err := base64.RawURLEncoding.DecodeString(expired.Secret)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(decodedSecret)
		if _, err := pool.Exec(ctx, `UPDATE auth_sessions SET created_at = now() - interval '2 seconds', expires_at = now() - interval '1 second' WHERE token_hash = $1`, hash[:]); err != nil {
			t.Fatal(err)
		}
		expiredResponse := servePushRequest(handler, http.MethodPost, "/push-subscriptions", expired.Secret, validPushSubscriptionJSON("expired"))
		assertCanonicalAuthError(t, expiredResponse, http.StatusUnauthorized, "unauthenticated")

		revoked, err := sessionStore.Create(ctx, userA, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if err := sessionStore.Revoke(ctx, revoked.Secret); err != nil {
			t.Fatal(err)
		}
		revokedResponse := servePushRequest(handler, http.MethodPost, "/push-subscriptions", revoked.Secret, validPushSubscriptionJSON("revoked"))
		assertCanonicalAuthError(t, revokedResponse, http.StatusUnauthorized, "unauthenticated")
	})

	t.Run("session store outage returns safe JSON 503", func(t *testing.T) {
		outageHandler := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, pool, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
			Store: sessionStoreForAPITest{secret: "outage-session", err: errors.New("connection refused")},
		})
		response := servePushRequest(outageHandler, http.MethodPost, "/push-subscriptions", "outage-session", validPushSubscriptionJSON("outage"))
		assertCanonicalAuthError(t, response, http.StatusServiceUnavailable, "authentication_unavailable")
	})

	t.Run("valid canonical session creates without trusting client identity or returning secrets", func(t *testing.T) {
		endpoint := "https://push.example.test/create"
		p256dh := validAPIIntegrationP256DH()
		authKey := validAPIIntegrationAuth()
		body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"` + p256dh + `","auth":"` + authKey + `"}}`
		response := servePushRequestWithHeaders(handler, http.MethodPost, "/push-subscriptions?user_id="+userB.String(), principalSession.Secret, body, map[string]string{"X-User-ID": userB.String()})
		if response.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), endpoint) || strings.Contains(response.Body.String(), p256dh) || strings.Contains(response.Body.String(), authKey) {
			t.Fatalf("response exposed subscription material: %s", response.Body.String())
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var storedUser uuid.UUID
		var storedEndpoint, storedP256DH, storedAuth string
		if err := pool.QueryRow(ctx, `SELECT user_id, endpoint, p256dh, auth FROM push_subscriptions WHERE id = $1`, result.ID).Scan(&storedUser, &storedEndpoint, &storedP256DH, &storedAuth); err != nil {
			t.Fatal(err)
		}
		if storedUser != userA || storedEndpoint != endpoint || storedP256DH != p256dh || storedAuth != authKey {
			t.Fatalf("stored subscription = %s/%q/%q/%q, want user A and submitted material", storedUser, storedEndpoint, storedP256DH, storedAuth)
		}
	})

	store := push.NewStore(pool)
	t.Run("valid canonical session revokes own subscription", func(t *testing.T) {
		subscription, _, err := store.Upsert(ctx, userA, apiIntegrationSubscriptionInput("revoke"))
		if err != nil {
			t.Fatal(err)
		}
		response := servePushRequest(handler, http.MethodDelete, "/push-subscriptions/"+subscription.ID.String(), principalSession.Secret, "")
		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", response.Code, response.Body.String())
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE id = $1`, subscription.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("subscription count = %d, want 0", count)
		}
	})

	t.Run("one user cannot revoke another user's subscription", func(t *testing.T) {
		subscription, _, err := store.Upsert(ctx, userA, apiIntegrationSubscriptionInput("cross-user"))
		if err != nil {
			t.Fatal(err)
		}
		otherSession, err := sessionStore.Create(ctx, userB, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		response := servePushRequest(handler, http.MethodDelete, "/push-subscriptions/"+subscription.ID.String(), otherSession.Secret, "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM push_subscriptions WHERE id = $1 AND user_id = $2`, subscription.ID, userA).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("cross-user subscription count = %d, want 1", count)
		}
	})
}

func servePushRequest(handler http.Handler, method, path, session, body string) *httptest.ResponseRecorder {
	return servePushRequestWithHeaders(handler, method, path, session, body, nil)
}

func servePushRequestWithHeaders(handler http.Handler, method, path, session, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if session != "" {
		request.AddCookie(&http.Cookie{Name: auth.DefaultCookieName, Value: session})
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertCanonicalAuthError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error = %#v, want code %q", body.Error, code)
	}
}

func validPushSubscriptionJSON(suffix string) string {
	return `{"endpoint":"https://push.example.test/` + suffix + `","keys":{"p256dh":"` + validAPIIntegrationP256DH() + `","auth":"` + validAPIIntegrationAuth() + `"}}`
}

func apiIntegrationSubscriptionInput(suffix string) push.SubscriptionInput {
	return push.SubscriptionInput{
		Endpoint: "https://push.example.test/" + suffix,
		P256DH:   validAPIIntegrationP256DH(),
		Auth:     validAPIIntegrationAuth(),
	}
}

func validAPIIntegrationP256DH() string {
	return base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
}

func validAPIIntegrationAuth() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 16))
}
