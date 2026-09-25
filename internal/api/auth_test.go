package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
)

type sessionStoreForAPITest struct {
	secret    string
	principal auth.Principal
	err       error
}

func (s sessionStoreForAPITest) Lookup(_ context.Context, secret string) (auth.Principal, error) {
	if s.err != nil {
		return auth.Principal{}, s.err
	}
	if secret != s.secret {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return s.principal, nil
}

func TestProtectedSessionContractReturnsJSON503ForStoreOutage(t *testing.T) {
	handler := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store: sessionStoreForAPITest{secret: "cookie-secret", err: errors.New("connection refused")},
	})
	request := httptest.NewRequest(http.MethodGet, "/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: "signa_session", Value: "cookie-secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "authentication_unavailable" {
		t.Fatalf("error code = %q, want authentication_unavailable", body.Error.Code)
	}
}

func TestProtectedSessionContractUsesCanonicalPrincipal(t *testing.T) {
	principal := auth.Principal{UserID: uuid.New()}
	handler := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store: sessionStoreForAPITest{secret: "cookie-secret", principal: principal},
	})

	request := httptest.NewRequest(http.MethodGet, "/auth/session?user_id="+uuid.NewString(), nil)
	request.AddCookie(&http.Cookie{Name: "signa_session", Value: "cookie-secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["user_id"] != principal.UserID.String() {
		t.Fatalf("user_id = %q, want %q", body["user_id"], principal.UserID)
	}
}

func TestProtectedSessionContractRejectsMissingCredentialWithoutAffectingPublicHealth(t *testing.T) {
	handler := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store: sessionStoreForAPITest{secret: "cookie-secret", principal: auth.Principal{UserID: uuid.New()}},
	})

	protected := httptest.NewRecorder()
	handler.ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/auth/session", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("protected status = %d, want 401", protected.Code)
	}
	if protected.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("protected Content-Type = %q, want application/json", protected.Header().Get("Content-Type"))
	}
	var errorBody errorResponse
	if err := json.Unmarshal(protected.Body.Bytes(), &errorBody); err != nil {
		t.Fatalf("decode protected error: %v", err)
	}
	if errorBody.Error.Code != "unauthenticated" || errorBody.Error.Message == "" {
		t.Fatalf("protected error = %#v", errorBody)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.Code)
	}
}
