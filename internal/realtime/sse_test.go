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

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/auth"
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
	principal := auth.Principal{UserID: uuid.New()}
	request := withPrincipal(httptest.NewRequest(http.MethodGet, "/events", nil), principal)
	NewHandler(source, ScopeAuthorizer{Resolver: testScopeResolver{incidents: map[string]bool{"incident-allowed": true}}}, time.Second).ServeHTTP(recorder, request)
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

func TestScopeAuthorizerAllowsNewAlertForAuthorizedIncident(t *testing.T) {
	source := &scriptedSource{results: []readResult{{events: []Event{{Stream: StreamAlert, RedisID: "1-0", Name: "alert.created.v1", AggregateID: "future-alert", IncidentID: "incident-allowed", Payload: `{"alert_id":"future-alert","incident_id":"incident-allowed"}`}}}, {}}}
	recorder := httptest.NewRecorder()
	request := withPrincipal(httptest.NewRequest(http.MethodGet, "/events", nil), auth.Principal{UserID: uuid.New()})
	NewHandler(source, ScopeAuthorizer{Resolver: testScopeResolver{incidents: map[string]bool{"incident-allowed": true}}}, time.Second).ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if !strings.Contains(body, "event: alert.created.v1\n") || !strings.Contains(body, `data: {"alert_id":"future-alert","incident_id":"incident-allowed"}`) {
		t.Fatalf("authorized alert missing: %q", recorder.Body.String())
	}
	if strings.Contains(body, "status_snapshot") || strings.Contains(body, "provider_response") {
		t.Fatalf("alert event expanded into authoritative/private fields: %q", body)
	}
}

func TestHandlerSendsHeartbeatAndCancelsSourceRead(t *testing.T) {
	source := &blockingSource{}
	base := httptest.NewRequest(http.MethodGet, "/events", nil)
	ctx, cancel := context.WithCancel(base.Context())
	request := withPrincipal(base.WithContext(ctx), auth.Principal{UserID: uuid.New()})
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
	principal := auth.Principal{UserID: uuid.New()}
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, withPrincipal(httptest.NewRequest(http.MethodGet, "/events", nil), principal))
	lastID := strings.Split(strings.Split(first.Body.String(), "id: ")[1], "\n")[0]
	secondRequest := httptest.NewRequest(http.MethodGet, "/events", nil)
	secondRequest.Header.Set("Last-Event-ID", lastID)
	handler.ServeHTTP(httptest.NewRecorder(), withPrincipal(secondRequest, principal))
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.reads) < 2 || source.reads[1].Alert != "9-0" {
		t.Fatalf("reads = %+v", source.reads)
	}
}

func TestHandlerRejectsMalformedReconnectCursor(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	request.Header.Set("Last-Event-ID", "not-base64")
	request = withPrincipal(request, auth.Principal{UserID: uuid.New()})
	recorder := httptest.NewRecorder()
	NewHandler(&scriptedSource{}, AllowAllAuthorizer{}, time.Second).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestHandlerRejectsInvalidReconnectStreamID(t *testing.T) {
	cursor, err := EncodeCursor(Cursor{Incident: "invalid", Alert: "$"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	request.Header.Set("Last-Event-ID", cursor)
	request = withPrincipal(request, auth.Principal{UserID: uuid.New()})
	recorder := httptest.NewRecorder()
	NewHandler(&scriptedSource{}, AllowAllAuthorizer{}, time.Second).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestHandlerStopsWhenApplicationShutsDown(t *testing.T) {
	shutdown, cancel := context.WithCancel(context.Background())
	source := &blockingSource{}
	handler := NewHandlerWithContext(shutdown, source, AllowAllAuthorizer{}, time.Second)
	request := withPrincipal(httptest.NewRequest(http.MethodGet, "/events", nil), auth.Principal{UserID: uuid.New()})
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for source.active.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop during shutdown")
	}
}

type blockingSource struct{ active atomicCounter }

func withPrincipal(request *http.Request, principal auth.Principal) *http.Request {
	return request.WithContext(auth.WithPrincipal(request.Context(), principal))
}

type testScopeResolver struct {
	incidents map[string]bool
	alerts    map[string]bool
}

func (r testScopeResolver) Authorize(_ context.Context, _ uuid.UUID, event Event) (bool, error) {
	if event.Stream == StreamIncident {
		return r.incidents[event.AggregateID], nil
	}
	if event.Stream == StreamAlert {
		return r.alerts[event.AggregateID] || r.incidents[event.IncidentID], nil
	}
	return false, nil
}

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
