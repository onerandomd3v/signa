package realtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedSource struct {
	mu      sync.Mutex
	reads   []Cursor
	results []readResult
}
type readResult struct {
	events []Event
	err    error
}

func (s *scriptedSource) Read(_ context.Context, cursor Cursor) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = append(s.reads, cursor)
	if len(s.results) == 0 {
		return nil, io.EOF
	}
	result := s.results[0]
	s.results = s.results[1:]
	return result.events, result.err
}

func TestHandlerRejectsUnauthenticatedRequest(t *testing.T) {
	handler := NewHandler(&scriptedSource{}, AllowAllAuthorizer{}, time.Second)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/events", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestHandlerStreamsAuthorizedEventsAndFiltersUnauthorizedEvents(t *testing.T) {
	source := &scriptedSource{results: []readResult{{events: []Event{{Stream: StreamIncident, RedisID: "1-0", Name: "incident.status_changed.v1", AggregateID: "incident-allowed", Payload: `{"status":"OPEN"}`}, {Stream: StreamIncident, RedisID: "2-0", Name: "incident.status_changed.v1", AggregateID: "incident-denied", Payload: `{"status":"OPEN"}`}}}}}
	recorder := httptest.NewRecorder()
	request := WithPrincipal(httptest.NewRequest(http.MethodGet, "/events", nil), Principal{UserID: "user-1", IncidentIDs: map[string]struct{}{"incident-allowed": {}}})
	NewHandler(source, ScopeAuthorizer{}, time.Second).ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("response = %d %v", recorder.Code, recorder.Header())
	}
	if !strings.Contains(body, "event: incident.status_changed.v1\ndata: {\"status\":\"OPEN\"}\n\n") {
		t.Fatalf("authorized event missing: %q", body)
	}
	if strings.Contains(body, "incident-denied") {
		t.Fatalf("unauthorized event leaked: %q", body)
	}
}

func TestHandlerSendsHeartbeatAndCancelsSourceRead(t *testing.T) {
	source := &blockingSource{}
	base := httptest.NewRequest(http.MethodGet, "/events", nil)
	ctx, cancel := context.WithCancel(base.Context())
	request := WithPrincipal(base.WithContext(ctx), Principal{UserID: "user-1"})
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		NewHandler(source, AllowAllAuthorizer{}, 5*time.Millisecond).ServeHTTP(recorder, request)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(recorder.Body.String(), ": heartbeat\n\n") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop")
	}
	if !strings.Contains(recorder.Body.String(), ": heartbeat\n\n") {
		t.Fatalf("heartbeat missing: %q", recorder.Body.String())
	}
	if source.active.Load() != 0 {
		t.Fatalf("active reads = %d", source.active.Load())
	}
}

func TestHandlerUsesLastEventIDCursorOnReconnect(t *testing.T) {
	source := &scriptedSource{results: []readResult{{events: []Event{{Stream: StreamAlert, RedisID: "9-0", Name: "alert.created.v1", AggregateID: "alert-1", Payload: `{}`}}}, {}}}
	handler := NewHandler(source, AllowAllAuthorizer{}, time.Second)
	principal := Principal{UserID: "user-1"}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, WithPrincipal(httptest.NewRequest(http.MethodGet, "/events", nil), principal))
	lastID := strings.Split(strings.Split(first.Body.String(), "id: ")[1], "\n")[0]
	secondRequest := httptest.NewRequest(http.MethodGet, "/events", nil)
	secondRequest.Header.Set("Last-Event-ID", lastID)
	handler.ServeHTTP(httptest.NewRecorder(), WithPrincipal(secondRequest, principal))
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.reads) < 2 || source.reads[1].Alert != "9-0" {
		t.Fatalf("reads = %+v", source.reads)
	}
}

func TestHandlerRejectsMalformedReconnectCursor(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	request.Header.Set("Last-Event-ID", "not-base64")
	request = WithPrincipal(request, Principal{UserID: "user-1"})
	recorder := httptest.NewRecorder()
	NewHandler(&scriptedSource{}, AllowAllAuthorizer{}, time.Second).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

type blockingSource struct{ active atomicCounter }

func (s *blockingSource) Read(ctx context.Context, _ Cursor) ([]Event, error) {
	s.active.Add(1)
	defer s.active.Add(-1)
	<-ctx.Done()
	return nil, ctx.Err()
}

type atomicCounter struct {
	mu    sync.Mutex
	value int
}

func (c *atomicCounter) Add(delta int) { c.mu.Lock(); c.value += delta; c.mu.Unlock() }
func (c *atomicCounter) Load() int     { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
