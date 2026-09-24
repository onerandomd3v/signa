package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goRedis "github.com/redis/go-redis/v9"
)

const (
	ReportEventsStream          = "signa:report-events"
	IncidentEventsStream        = "signa:incident-events"
	MediaEventsStream           = "signa:media-events"
	ReportCreatedV1             = "report.created.v1"
	MediaAttachedV1             = "report.media_attached.v1"
	ReportAIProcessedV1         = "report.ai_processed.v1"
	IncidentCreatedV1           = "incident.created.v1"
	IncidentReportAttachedV1    = "incident.report_attached.v1"
	IncidentConfidenceChangedV1 = "incident.confidence_changed.v1"
	IncidentSeverityChangedV1   = "incident.severity_changed.v1"
	DefaultBatchSize            = 100
	maxErrorLength              = 1000
)

type RedisStream interface {
	XAdd(context.Context, *goRedis.XAddArgs) *goRedis.StringCmd
}

type Event struct {
	ID            string
	EventType     string
	AggregateType string
	AggregateID   string
	Payload       json.RawMessage
	CreatedAt     time.Time
}

type StreamEvent struct {
	EventID       string
	EventName     string
	AggregateType string
	AggregateID   string
	OccurredAt    string
	Payload       string
}

type Publisher struct {
	pool      *pgxpool.Pool
	redis     RedisStream
	logger    *slog.Logger
	stream    string
	batchSize int
}

func NewPublisher(pool *pgxpool.Pool, redis RedisStream, logger *slog.Logger) *Publisher {
	return &Publisher{pool: pool, redis: redis, logger: logger, stream: ReportEventsStream, batchSize: DefaultBatchSize}
}

func (p *Publisher) PublishCycle(ctx context.Context) error {
	if p.pool == nil || p.redis == nil {
		return fmt.Errorf("outbox publisher dependencies are required")
	}
	attempted := make(map[string]struct{}, p.batchSize)
	var firstErr error
	for len(attempted) < p.batchSize {
		event, published, err := p.publishOne(ctx, attempted)
		if event != nil {
			attempted[event.ID] = struct{}{}
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if event == nil {
				return firstErr
			}
			continue
		}
		if event == nil {
			break
		}
		if published {
			p.log("outbox event published", slog.String("event_id", event.ID), slog.String("event_name", eventName(event)), slog.String("aggregate_id", event.AggregateID))
		}
	}
	return firstErr
}

func (p *Publisher) publishOne(ctx context.Context, attempted map[string]struct{}) (*Event, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin outbox transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	query := `SELECT id::text, event_type, aggregate_type, aggregate_id::text, payload, created_at
		FROM outbox_events WHERE published_at IS NULL AND event_type IN ('report.created', 'report.media_attached', 'report.ai_processed', 'incident.created', 'incident.report_attached', 'incident.confidence_changed', 'incident.severity_changed')`
	args := make([]any, 0, len(attempted))
	if len(attempted) > 0 {
		placeholders := make([]string, 0, len(attempted))
		for id := range attempted {
			args = append(args, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		query += " AND id::text NOT IN (" + strings.Join(placeholders, ", ") + ")"
	}
	query += " ORDER BY created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED"
	event := &Event{}
	if err := tx.QueryRow(ctx, query, args...).Scan(&event.ID, &event.EventType, &event.AggregateType, &event.AggregateID, &event.Payload, &event.CreatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("claim outbox event: %w", err)
	}

	streamEvent, err := MapEvent(*event)
	if err != nil {
		return event, false, p.recordFailure(ctx, tx, event, err)
	}
	stream := p.stream
	if event.EventType == "report.media_attached" && stream == ReportEventsStream {
		stream = MediaEventsStream
	}
	if (event.EventType == "incident.created" || event.EventType == "incident.report_attached" || event.EventType == "incident.confidence_changed" || event.EventType == "incident.severity_changed") && stream == ReportEventsStream {
		stream = IncidentEventsStream
	}
	_, err = p.redis.XAdd(ctx, &goRedis.XAddArgs{Stream: stream, Values: map[string]any{
		"event_id": streamEvent.EventID, "event_name": streamEvent.EventName, "aggregate_type": streamEvent.AggregateType,
		"aggregate_id": streamEvent.AggregateID, "occurred_at": streamEvent.OccurredAt, "payload": streamEvent.Payload,
	}}).Result()
	if err != nil {
		return event, false, p.recordFailure(ctx, tx, event, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox_events SET published_at = now(), last_error = NULL WHERE id = $1`, event.ID); err != nil {
		_ = tx.Rollback(ctx)
		return event, false, p.recordAcknowledgementFailure(ctx, event, fmt.Errorf("update published outbox event %s: %w", event.ID, err))
	}
	if err := tx.Commit(ctx); err != nil {
		return event, false, p.recordAcknowledgementFailure(ctx, event, fmt.Errorf("commit published outbox event %s: %w", event.ID, err))
	}
	return event, true, nil
}

func (p *Publisher) recordAcknowledgementFailure(ctx context.Context, event *Event, acknowledgementErr error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("record acknowledgement failure for outbox event %s: %w (original: %v)", event.ID, err, acknowledgementErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var retryAttempt int
	err = tx.QueryRow(ctx, `
		UPDATE outbox_events
		SET retry_attempts = retry_attempts + 1, last_error = $2
		WHERE id = $1 AND published_at IS NULL
		RETURNING retry_attempts
	`, event.ID, truncateError(acknowledgementErr.Error())).Scan(&retryAttempt)
	if err == pgx.ErrNoRows {
		// The original commit may have succeeded even if its client observed an error.
		return acknowledgementErr
	}
	if err != nil {
		return fmt.Errorf("record acknowledgement failure for outbox event %s: %w (original: %v)", event.ID, err, acknowledgementErr)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit acknowledgement failure for outbox event %s: %w (original: %v)", event.ID, err, acknowledgementErr)
	}
	p.log("outbox acknowledgement failed", slog.String("event_id", event.ID), slog.String("event_name", eventName(event)), slog.String("aggregate_id", event.AggregateID), slog.Int("retry_attempt", retryAttempt), slog.String("error", truncateError(acknowledgementErr.Error())))
	return acknowledgementErr
}

func (p *Publisher) recordFailure(ctx context.Context, tx pgx.Tx, event *Event, publishErr error) error {
	message := truncateError(publishErr.Error())
	var retryAttempt int
	if err := tx.QueryRow(ctx, `UPDATE outbox_events SET retry_attempts = retry_attempts + 1, last_error = $2 WHERE id = $1 RETURNING retry_attempts`, event.ID, message).Scan(&retryAttempt); err != nil {
		return fmt.Errorf("record outbox failure %s: %w (original: %s)", event.ID, err, message)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit outbox failure %s: %w", event.ID, err)
	}
	p.log("outbox event publish failed", slog.String("event_id", event.ID), slog.String("event_name", eventName(event)), slog.String("aggregate_id", event.AggregateID), slog.Int("retry_attempt", retryAttempt), slog.String("error", message))
	return fmt.Errorf("publish outbox event %s: %w", event.ID, publishErr)
}

func MapEvent(event Event) (StreamEvent, error) {
	eventName := map[string]string{
		"report.created":              ReportCreatedV1,
		"report.media_attached":       MediaAttachedV1,
		"report.ai_processed":         ReportAIProcessedV1,
		"incident.created":            IncidentCreatedV1,
		"incident.report_attached":    IncidentReportAttachedV1,
		"incident.confidence_changed": IncidentConfidenceChangedV1,
		"incident.severity_changed":   IncidentSeverityChangedV1,
	}[event.EventType]
	if eventName == "" {
		return StreamEvent{}, fmt.Errorf("unsupported outbox event type %q", event.EventType)
	}
	return StreamEvent{EventID: event.ID, EventName: eventName, AggregateType: event.AggregateType, AggregateID: event.AggregateID, OccurredAt: event.CreatedAt.UTC().Format(time.RFC3339Nano), Payload: string(event.Payload)}, nil
}

func eventName(event *Event) string {
	switch event.EventType {
	case "report.created":
		return ReportCreatedV1
	case "report.media_attached":
		return MediaAttachedV1
	case "report.ai_processed":
		return ReportAIProcessedV1
	case "incident.created":
		return IncidentCreatedV1
	case "incident.report_attached":
		return IncidentReportAttachedV1
	case "incident.confidence_changed":
		return IncidentConfidenceChangedV1
	case "incident.severity_changed":
		return IncidentSeverityChangedV1
	}
	return event.EventType
}

func truncateError(message string) string {
	if len(message) <= maxErrorLength {
		return message
	}
	return message[:maxErrorLength]
}

func (p *Publisher) log(message string, args ...any) {
	if p.logger != nil {
		p.logger.Info(message, args...)
	}
}
