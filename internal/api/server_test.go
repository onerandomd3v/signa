package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/realtime"
)

func TestHealthz(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	NewHandler(nil, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status body = %q, want ok", body["status"])
	}
}

type emptyRealtimeSource struct{}

func (emptyRealtimeSource) Read(context.Context, realtime.Cursor) ([]realtime.Event, error) {
	return nil, io.EOF
}

func TestRealtimeRouteRequiresAuthenticationAndIsMounted(t *testing.T) {
	h := realtime.NewHandler(emptyRealtimeSource{}, realtime.AllowAllAuthorizer{}, time.Second)
	server := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store: sessionStoreForAPITest{secret: "cookie-secret", principal: auth.Principal{UserID: uuid.New()}},
	}, h)
	unauthenticated := httptest.NewRecorder()
	server.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/events", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", unauthenticated.Code)
	}
	authenticated := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	request.AddCookie(&http.Cookie{Name: auth.DefaultCookieName, Value: "cookie-secret"})
	server.ServeHTTP(authenticated, request)
	if authenticated.Code != http.StatusOK || authenticated.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("response = %d %v", authenticated.Code, authenticated.Header())
	}
}

func TestRealtimeRouteUsesCanonicalSessionFailures(t *testing.T) {
	h := realtime.NewHandler(emptyRealtimeSource{}, realtime.AllowAllAuthorizer{}, time.Second)
	server := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store: sessionStoreForAPITest{secret: "valid-secret", principal: auth.Principal{UserID: uuid.New()}},
	}, h)

	for _, test := range []struct {
		name   string
		cookie *http.Cookie
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "invalid", cookie: &http.Cookie{Name: auth.DefaultCookieName, Value: "wrong"}, status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/events", nil)
			if test.cookie != nil {
				request.AddCookie(test.cookie)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("response = %d %v", response.Code, response.Header())
			}
		})
	}

	outageServer := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store: sessionStoreForAPITest{secret: "valid-secret", err: errors.New("connection refused")},
	}, h)
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	request.AddCookie(&http.Cookie{Name: auth.DefaultCookieName, Value: "valid-secret"})
	response := httptest.NewRecorder()
	outageServer.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("outage status = %d, want 503", response.Code)
	}
}

func TestHealthzMethodNotFound(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)

	NewHandler(nil, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestNewServerTimeouts(t *testing.T) {
	server := NewServer(":8080", nil, nil)

	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 5s", server.ReadHeaderTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout = %s, want 60s", server.IdleTimeout)
	}
	if server.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout = %s, want 30s", server.ReadTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want zero for future SSE compatibility", server.WriteTimeout)
	}
}
