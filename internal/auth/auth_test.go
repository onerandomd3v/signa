package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeSessionStore struct {
	secret    string
	principal Principal
	err       error
	lookups   int
}

func (s *fakeSessionStore) Lookup(_ context.Context, secret string) (Principal, error) {
	s.lookups++
	if s.err != nil {
		return Principal{}, s.err
	}
	if secret != s.secret {
		return Principal{}, ErrUnauthenticated
	}
	return s.principal, nil
}

func TestPrincipalContextRoundTrip(t *testing.T) {
	principal := Principal{UserID: uuid.New()}
	ctx := WithPrincipal(context.Background(), principal)

	got, ok := PrincipalFromContext(ctx)
	if !ok || got != principal {
		t.Fatalf("PrincipalFromContext() = %#v, %v, want %#v, true", got, ok, principal)
	}
}

func TestMiddlewareRejectsMissingMalformedAndInvalidCredentials(t *testing.T) {
	store := &fakeSessionStore{secret: "valid-secret", principal: Principal{UserID: uuid.New()}}
	handler := RequirePrincipal(store)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		name   string
		cookie *http.Cookie
	}{
		{name: "missing"},
		{name: "malformed", cookie: &http.Cookie{Name: DefaultCookieName, Value: "bad;secret"}},
		{name: "invalid", cookie: &http.Cookie{Name: DefaultCookieName, Value: "not-the-session"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if test.cookie != nil {
				request.AddCookie(test.cookie)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
		})
	}
}

func TestMiddlewareInjectsCanonicalPrincipalAndIgnoresClientUserID(t *testing.T) {
	principal := Principal{UserID: uuid.New()}
	store := &fakeSessionStore{secret: "valid-secret", principal: principal}
	var got Principal
	handler := RequirePrincipal(store)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var ok bool
		got, ok = PrincipalFromContext(request.Context())
		if !ok {
			t.Fatal("protected handler did not receive principal")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/protected?user_id="+uuid.NewString(), nil)
	request.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "valid-secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if got != principal {
		t.Fatalf("principal = %#v, want %#v", got, principal)
	}
}

func TestMiddlewareReturnsJSONServiceUnavailableForOperationalStoreErrors(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	store := &fakeSessionStore{secret: "valid-secret", err: errors.New("connection refused")}
	handler := RequirePrincipalWithLogger(store, logger)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatal("protected handler ran during store outage")
	}))
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.AddCookie(&http.Cookie{Name: DefaultCookieName, Value: "valid-secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	if strings.Contains(response.Body.String(), "valid-secret") || strings.Contains(logs.String(), "valid-secret") {
		t.Fatal("session credential leaked in response or logs")
	}
}

func TestCookieOptionsAreExplicitAndCredentialIsNotLoggedInErrors(t *testing.T) {
	expiresAt := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	secret, err := NewSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := NewSessionCookie(CookieOptions{
		Name:     "signa_session",
		Path:     "/",
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	}, secret, expiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if cookie.HttpOnly != true || !cookie.Secure || cookie.SameSite != http.SameSiteNoneMode || cookie.Path != "/" || cookie.Expires != expiresAt {
		t.Fatalf("cookie security attributes = %#v", cookie)
	}
	_, invalidSecretErr := NewSessionCookie(CookieOptions{Name: "signa_session", Path: "/", Secure: true, SameSite: http.SameSiteNoneMode}, "not-a-session-secret", expiresAt)
	if invalidSecretErr == nil || strings.Contains(invalidSecretErr.Error(), "not-a-session-secret") {
		t.Fatal("credential leaked in error")
	}
	cleared, err := ClearSessionCookie(CookieOptions{Name: "signa_session", Path: "/", Secure: true, SameSite: http.SameSiteNoneMode})
	if err != nil || cleared.MaxAge != -1 || cleared.Value != "" || !cleared.HttpOnly {
		t.Fatalf("clear cookie = %#v, error = %v", cleared, err)
	}
	if _, err := NewSessionCookie(CookieOptions{Name: ""}, secret, expiresAt); !errors.Is(err, ErrInvalidCookieOptions) {
		t.Fatalf("invalid cookie options error = %v, want %v", err, ErrInvalidCookieOptions)
	}
}
