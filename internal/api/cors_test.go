package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/reports"
)

func TestCORSAllowedPreflightAndPost(t *testing.T) {
	ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}}
	handler := NewHandlerWithCORS(nil, RateLimitConfig{PerClientRatePerMinute: 1, PerClientBurst: 1, GlobalRatePerMinute: 1, GlobalBurst: 1}, []string{"http://localhost:3000"}, ingestor)

	preflight := httptest.NewRequest(http.MethodOptions, "/reports", nil)
	preflight.Header.Set("Origin", "http://localhost:3000")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type, idempotency-key")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)

	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", preflightResponse.Code)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Methods"); got != "POST" {
		t.Fatalf("allow methods = %q", got)
	}
	if got := preflightResponse.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Idempotency-Key" {
		t.Fatalf("allow headers = %q", got)
	}
	if got := preflightResponse.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary = %q", got)
	}
	if ingestor.calls != 0 {
		t.Fatalf("preflight ingestor calls = %d, want 0", ingestor.calls)
	}

	post := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(`{"raw_text":"report"}`))
	post.RemoteAddr = "10.0.0.1:1000"
	post.Header.Set("Origin", "http://localhost:3000")
	post.Header.Set("Idempotency-Key", "cors-key")
	postResponse := httptest.NewRecorder()
	handler.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusAccepted || ingestor.calls != 1 {
		t.Fatalf("allowed POST status/calls = %d/%d", postResponse.Code, ingestor.calls)
	}
	if got := postResponse.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("POST allow origin = %q", got)
	}
}

func TestCORSPreflightDoesNotConsumeRateLimitCapacity(t *testing.T) {
	ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}}
	handler := NewHandlerWithCORS(nil, RateLimitConfig{PerClientRatePerMinute: 1, PerClientBurst: 1, GlobalRatePerMinute: 1, GlobalBurst: 1}, []string{"http://localhost:3000"}, ingestor)
	preflight := httptest.NewRequest(http.MethodOptions, "/reports", nil)
	preflight.Header.Set("Origin", "http://localhost:3000")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Content-Type, Idempotency-Key")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)

	post := func(key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(`{"raw_text":"report"}`))
		request.RemoteAddr = "10.0.0.2:1000"
		request.Header.Set("Origin", "http://localhost:3000")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := post("first"); response.Code != http.StatusAccepted {
		t.Fatalf("POST after preflight status = %d, want 202", response.Code)
	}
	if response := post("second"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("second POST status = %d, want 429", response.Code)
	}
}

func TestCORSDisallowedAndSpoofedOriginsReceiveNoAuthorizationHeaders(t *testing.T) {
	for _, origin := range []string{"http://evil.test", "http://localhost:3000.evil.test", "http://evil.localhost:3000"} {
		ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}}
		handler := NewHandlerWithCORS(nil, DefaultRateLimitConfig(), []string{"http://localhost:3000"}, ingestor)
		request := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(`{"raw_text":"report"}`))
		request.Header.Set("Origin", origin)
		request.Header.Set("Idempotency-Key", "origin-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Header().Get("Access-Control-Allow-Origin") != "" || response.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("origin %q received CORS authorization headers: %v", origin, response.Header())
		}
	}
}

func TestCORSRequestWithoutOriginContinuesNormally(t *testing.T) {
	ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}}
	handler := NewHandlerWithCORS(nil, DefaultRateLimitConfig(), []string{"http://localhost:3000"}, ingestor)
	request := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(`{"raw_text":"report"}`))
	request.Header.Set("Idempotency-Key", "no-origin-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || ingestor.calls != 1 {
		t.Fatalf("no-origin status/calls = %d/%d", response.Code, ingestor.calls)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no-origin request received CORS authorization header")
	}
}

func TestCORSAllowsMultipleConfiguredOrigins(t *testing.T) {
	handler := NewHandlerWithCORS(nil, DefaultRateLimitConfig(), []string{"http://localhost:3000", "https://staging.signa.test"}, &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}})
	for _, origin := range []string{"http://localhost:3000", "https://staging.signa.test"} {
		request := httptest.NewRequest(http.MethodOptions, "/reports", nil)
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatalf("origin %q status/header = %d/%q", origin, response.Code, response.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}
