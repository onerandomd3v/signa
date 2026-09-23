package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onerandomd3v/signa/internal/reports"
)

func TestReportRateLimitRejectsBurstBeforeIngesting(t *testing.T) {
	ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}}
	handler := NewHandlerWithRateLimit(nil, RateLimitConfig{PerClientRatePerMinute: 6, PerClientBurst: 2, GlobalRatePerMinute: 120, GlobalBurst: 30}, ingestor)

	for i := 0; i < 2; i++ {
		response := performRequestWithRemote(t, handler, "10.0.0.1:1000", "key-"+string(rune('a'+i)), `{"raw_text":"report"}`)
		if response.Code != http.StatusAccepted {
			t.Fatalf("request %d status = %d, want 202", i, response.Code)
		}
	}
	limited := performRequestWithRemote(t, handler, "10.0.0.1:1000", "key-c", `{"raw_text":"report"}`)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", limited.Code)
	}
	assertErrorResponse(t, limited, "rate_limited")
	if limited.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After header is missing")
	}
	if ingestor.calls != 2 {
		t.Fatalf("ingestor calls = %d, want 2", ingestor.calls)
	}
}

func TestReportRateLimitSeparatesClientsAndIgnoresForwardingHeaders(t *testing.T) {
	ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}}
	handler := NewHandlerWithRateLimit(nil, RateLimitConfig{PerClientRatePerMinute: 1, PerClientBurst: 1, GlobalRatePerMinute: 120, GlobalBurst: 30}, ingestor)

	first := performRequestWithRemoteAndHeaders(t, handler, "10.0.0.1:1000", "key-a", map[string]string{"X-Forwarded-For": "10.0.0.2", "X-Real-IP": "10.0.0.2"})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", first.Code)
	}
	second := performRequestWithRemoteAndHeaders(t, handler, "10.0.0.2:1001", "key-b", map[string]string{"X-Forwarded-For": "10.0.0.1"})
	if second.Code != http.StatusAccepted {
		t.Fatalf("different client status = %d", second.Code)
	}
	bypassed := performRequestWithRemoteAndHeaders(t, handler, "10.0.0.1:1002", "key-c", map[string]string{"X-Forwarded-For": "10.0.0.99", "X-Real-IP": "10.0.0.99"})
	if bypassed.Code != http.StatusTooManyRequests {
		t.Fatalf("forwarding-header request status = %d, want 429", bypassed.Code)
	}
}

func TestGlobalReportRateLimitStopsAggregateTraffic(t *testing.T) {
	ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1"}}
	handler := NewHandlerWithRateLimit(nil, RateLimitConfig{PerClientRatePerMinute: 120, PerClientBurst: 30, GlobalRatePerMinute: 2, GlobalBurst: 2}, ingestor)
	for i := 0; i < 2; i++ {
		response := performRequestWithRemote(t, handler, "10.0.0."+string(rune('1'+i))+":1000", "key-"+string(rune('a'+i)), `{"raw_text":"report"}`)
		if response.Code != http.StatusAccepted {
			t.Fatalf("request %d status = %d", i, response.Code)
		}
	}
	limited := performRequestWithRemote(t, handler, "10.0.0.3:1000", "key-c", `{"raw_text":"report"}`)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", limited.Code)
	}
	if ingestor.calls != 2 {
		t.Fatalf("ingestor calls = %d, want 2", ingestor.calls)
	}
}

func TestHealthzIsNotRateLimited(t *testing.T) {
	handler := NewHandlerWithRateLimit(nil, RateLimitConfig{PerClientRatePerMinute: 1, PerClientBurst: 1, GlobalRatePerMinute: 1, GlobalBurst: 1}, nil)
	for i := 0; i < 3; i++ {
		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		request.RemoteAddr = "10.0.0.1:1000"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("health status = %d, want 200", response.Code)
		}
	}
}

func TestIdleClientLimitersAreCleanedUp(t *testing.T) {
	current := time.Unix(100, 0)
	limiter := NewRateLimiter(RateLimitConfig{PerClientRatePerMinute: 6, PerClientBurst: 1, GlobalRatePerMinute: 120, GlobalBurst: 30}, RateLimiterOptions{Now: func() time.Time { return current }, IdleAfter: time.Minute})
	limiter.Allow("client-1")
	if limiter.ClientCount() != 1 {
		t.Fatalf("client count = %d, want 1", limiter.ClientCount())
	}
	current = current.Add(time.Minute)
	limiter.Allow("client-2")
	if limiter.ClientCount() != 1 {
		t.Fatalf("client count after cleanup = %d, want 1", limiter.ClientCount())
	}
}

func TestRemoteAddrClientKeyDoesNotTrustForwardingHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/reports", nil)
	request.RemoteAddr = "[2001:db8::1]:443"
	request.Header.Set("X-Forwarded-For", "2001:db8::2")
	request.Header.Set("X-Real-IP", "2001:db8::2")
	if got := RemoteAddrClientKey(request); got != "2001:db8::1" {
		t.Fatalf("client key = %q", got)
	}
}

func TestReportRejectsMoreThanTenThousandUnicodeCharacters(t *testing.T) {
	ingestor := &fakeIngestor{}
	recorder := performReportRequest(t, ingestor, "key-long", `{"raw_text":"`+strings.Repeat("界", 10001)+`"}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	assertErrorResponse(t, recorder, "invalid_request")
	if ingestor.calls != 0 {
		t.Fatalf("ingestor calls = %d, want 0", ingestor.calls)
	}
}

func performRequestWithRemote(t *testing.T, handler http.Handler, remoteAddr, key, body string) *httptest.ResponseRecorder {
	return performRequestWithBodyAndHeaders(t, handler, remoteAddr, key, body, nil)
}

func performRequestWithRemoteAndHeaders(t *testing.T, handler http.Handler, remoteAddr, key string, headers map[string]string) *httptest.ResponseRecorder {
	return performRequestWithBodyAndHeaders(t, handler, remoteAddr, key, `{"raw_text":"report"}`, headers)
}

func performRequestWithBodyAndHeaders(t *testing.T, handler http.Handler, remoteAddr, key, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(body))
	request.RemoteAddr = remoteAddr
	request.Header.Set("Idempotency-Key", key)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
