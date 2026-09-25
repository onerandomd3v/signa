package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onerandomd3v/signa/internal/outbox"
	goRedis "github.com/redis/go-redis/v9"
)

const (
	StreamIncident = "incident"
	StreamAlert    = "alert"
	defaultCount   = 100
)

type Principal struct {
	UserID      string
	IncidentIDs map[string]struct{}
	AlertIDs    map[string]struct{}
}

type principalKey struct{}

func WithPrincipal(request *http.Request, principal Principal) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), principalKey{}, principal))
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok && strings.TrimSpace(principal.UserID) != ""
}

type Event struct {
	Stream      string
	RedisID     string
	EventID     string
	Name        string
	AggregateID string
	OccurredAt  string
	Payload     string
}

type Cursor struct {
	Incident string `json:"incident"`
	Alert    string `json:"alert"`
}

type Source interface {
	Read(context.Context, Cursor) ([]Event, error)
}
type Authorizer interface {
	Authorize(context.Context, Principal, Event) bool
}

type AllowAllAuthorizer struct{}

func (AllowAllAuthorizer) Authorize(context.Context, Principal, Event) bool { return true }

type ScopeAuthorizer struct{}

func (ScopeAuthorizer) Authorize(_ context.Context, principal Principal, event Event) bool {
	if event.Stream == StreamIncident {
		_, ok := principal.IncidentIDs[event.AggregateID]
		return ok
	}
	if event.Stream == StreamAlert {
		_, ok := principal.AlertIDs[event.AggregateID]
		return ok
	}
	return false
}

type Handler struct {
	source     Source
	authorizer Authorizer
	heartbeat  time.Duration
}

func NewHandler(source Source, authorizer Authorizer, heartbeat time.Duration) *Handler {
	if authorizer == nil {
		authorizer = ScopeAuthorizer{}
	}
	return &Handler{source: source, authorizer: authorizer, heartbeat: heartbeat}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	principal, ok := PrincipalFromContext(request.Context())
	if !ok {
		writeJSONError(writer, http.StatusUnauthorized, "authentication_required")
		return
	}
	if h == nil || h.source == nil || h.heartbeat <= 0 {
		writeJSONError(writer, http.StatusServiceUnavailable, "realtime_unavailable")
		return
	}
	cursor, err := DecodeCursor(request.Header.Get("Last-Event-ID"))
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "invalid_last_event_id")
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeJSONError(writer, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	_, _ = writer.Write([]byte(": connected\n\n"))
	flusher.Flush()

	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	readCh := make(chan sourceReadResult, 1)
	startRead := func(current Cursor) {
		go func() {
			events, readErr := h.source.Read(request.Context(), current)
			readCh <- sourceReadResult{events: events, err: readErr}
		}()
	}
	startRead(cursor)
	for {
		select {
		case <-request.Context().Done():
			// Source implementations must honor the request context. Waiting for
			// the in-flight read to return makes cancellation cleanup observable.
			<-readCh
			return
		case <-ticker.C:
			_, _ = writer.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		case result := <-readCh:
			if result.err != nil {
				return
			}
			for _, event := range result.events {
				if err := advanceCursor(&cursor, event); err != nil {
					return
				}
				if !h.authorizer.Authorize(request.Context(), principal, event) {
					continue
				}
				if err := writeEvent(writer, flusher, cursor, event); err != nil {
					return
				}
			}
			startRead(cursor)
		}
	}
}

type sourceReadResult struct {
	events []Event
	err    error
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.Incident == "" || cursor.Alert == "" {
		return "", errors.New("both stream cursors are required")
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeCursor(value string) (Cursor, error) {
	if value == "" {
		return Cursor{Incident: "$", Alert: "$"}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var cursor Cursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.Incident == "" || cursor.Alert == "" {
		return Cursor{}, errors.New("cursor must contain incident and alert IDs")
	}
	return cursor, nil
}

func advanceCursor(cursor *Cursor, event Event) error {
	if event.RedisID == "" {
		return errors.New("stream event ID is required")
	}
	switch event.Stream {
	case StreamIncident:
		cursor.Incident = event.RedisID
	case StreamAlert:
		cursor.Alert = event.RedisID
	default:
		return fmt.Errorf("unsupported event stream %q", event.Stream)
	}
	return nil
}

func writeEvent(writer http.ResponseWriter, flusher http.Flusher, cursor Cursor, event Event) error {
	if strings.ContainsAny(event.Name, "\r\n") {
		return errors.New("event name contains a newline")
	}
	id, err := EncodeCursor(cursor)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "id: %s\nevent: %s\n", id, event.Name); err != nil {
		return err
	}
	for _, line := range strings.Split(event.Payload, "\n") {
		if _, err := fmt.Fprintf(writer, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

type RedisSource struct {
	Client *goRedis.Client
	Block  time.Duration
	Count  int
}

func (s *RedisSource) Read(ctx context.Context, cursor Cursor) ([]Event, error) {
	if s == nil || s.Client == nil {
		return nil, errors.New("redis source is not configured")
	}
	block := s.Block
	if block <= 0 {
		block = 25 * time.Second
	}
	count := s.Count
	if count <= 0 {
		count = defaultCount
	}
	entries, err := s.Client.XRead(ctx, &goRedis.XReadArgs{Streams: []string{outbox.IncidentEventsStream, cursor.Incident, outbox.AlertEventsStream, cursor.Alert}, Count: int64(count), Block: block}).Result()
	if err != nil {
		if errors.Is(err, goRedis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]Event, 0)
	for _, stream := range entries {
		streamName := StreamIncident
		if stream.Stream == outbox.AlertEventsStream {
			streamName = StreamAlert
		}
		for _, message := range stream.Messages {
			result = append(result, Event{Stream: streamName, RedisID: message.ID, EventID: stringField(message.Values, "event_id"), Name: stringField(message.Values, "event_name"), AggregateID: stringField(message.Values, "aggregate_id"), OccurredAt: stringField(message.Values, "occurred_at"), Payload: stringField(message.Values, "payload")})
		}
	}
	return result, nil
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func writeJSONError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]string{"code": code, "message": code}})
}
