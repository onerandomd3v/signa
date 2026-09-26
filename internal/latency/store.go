package latency

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const MaxRecentSample = 100

var ErrReportNotFound = errors.New("report trace was not found")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) TraceReport(ctx context.Context, reportID uuid.UUID) (Timeline, error) {
	if reportID == uuid.Nil {
		return Timeline{}, errors.New("report id is required")
	}
	if s == nil || s.pool == nil {
		return Timeline{}, errors.New("latency database is required")
	}
	items, err := s.query(ctx, &reportID, 1)
	if err != nil {
		return Timeline{}, err
	}
	if len(items) == 0 {
		return Timeline{}, ErrReportNotFound
	}
	return items[0], nil
}

func (s *Store) Recent(ctx context.Context, limit int) ([]Timeline, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("latency database is required")
	}
	if err := validateSampleLimit(limit); err != nil {
		return nil, err
	}
	return s.query(ctx, nil, limit)
}

func validateSampleLimit(limit int) error {
	if limit < 1 || limit > MaxRecentSample {
		return fmt.Errorf("sample limit must be between 1 and %d", MaxRecentSample)
	}
	return nil
}

func safeFailureKind(value string) string {
	switch value {
	case "transient", "permanent":
		return value
	default:
		return "unknown"
	}
}

const timelineQuery = `
WITH selected_reports AS (
	SELECT r.id
	FROM reports AS r
	WHERE ($1::uuid IS NULL OR r.id = $1)
	ORDER BY r.created_at DESC, r.id DESC
	LIMIT $2
)
SELECT
	r.id::text,
	r.submitted_at,
	r.created_at,
	r.incident_id::text,
	re.created_at,
	re.published_at,
	COALESCE(re.retry_attempts, 0),
	COALESCE(re.last_error IS NOT NULL AND re.last_error <> '', false),
	aip.state,
	aip.started_at,
	aip.completed_at,
	aip.last_failed_at,
	aip.failure_kind,
	COALESCE(aip.attempts, 0),
	aie.created_at,
	COALESCE(ir.incident_id, r.incident_id)::text,
	ir.attached_at,
	a.id::text,
	a.created_at,
	d.id::text,
	d.state,
	d.created_at,
	d.last_attempt_at,
	d.delivered_at,
	COALESCE(d.attempts, 0),
	da.state,
	da.failure_kind,
	da.started_at,
	da.completed_at,
	da_first.started_at
FROM selected_reports AS selected
JOIN reports AS r ON r.id = selected.id
LEFT JOIN LATERAL (
	SELECT created_at, published_at, retry_attempts, last_error
	FROM outbox_events
	WHERE event_type = 'report.created' AND aggregate_type = 'report' AND aggregate_id = r.id
	ORDER BY created_at, id
	LIMIT 1
) AS re ON true
LEFT JOIN report_ai_processing AS aip ON aip.report_id = r.id
LEFT JOIN LATERAL (
	SELECT created_at
	FROM report_ai_extractions
	WHERE report_id = r.id
	ORDER BY created_at DESC, id DESC
	LIMIT 1
) AS aie ON true
LEFT JOIN LATERAL (
	SELECT incident_id, attached_at
	FROM incident_reports
	WHERE report_id = r.id
	ORDER BY attached_at, incident_id
	LIMIT 1
) AS ir ON true
LEFT JOIN LATERAL (
	SELECT id, created_at
	FROM alerts
	WHERE incident_id = COALESCE(ir.incident_id, r.incident_id)
	  AND created_at >= COALESCE(ir.attached_at, r.created_at)
	ORDER BY created_at, id
	LIMIT 1
) AS a ON true
LEFT JOIN LATERAL (
	SELECT id, state, created_at, last_attempt_at, delivered_at, attempts
	FROM deliveries
	WHERE alert_id = a.id
	ORDER BY created_at, id
	LIMIT 1
) AS d ON true
LEFT JOIN LATERAL (
	SELECT state, failure_kind, started_at, completed_at
	FROM delivery_attempts
	WHERE delivery_id = d.id
	ORDER BY attempt_no DESC
	LIMIT 1
) AS da ON true
LEFT JOIN LATERAL (
	SELECT started_at
	FROM delivery_attempts
	WHERE delivery_id = d.id
	ORDER BY attempt_no
	LIMIT 1
) AS da_first ON true
ORDER BY r.created_at DESC, r.id DESC
`

type timelineRow struct {
	reportID                                                    string
	submittedAt, reportCreatedAt                                time.Time
	linkedIncidentID                                            *string
	outboxCreatedAt, outboxPublishedAt                          *time.Time
	outboxRetries                                               int
	outboxHasError                                              bool
	aiState                                                     *string
	aiStartedAt, aiCompletedAt, aiFailedAt                      *time.Time
	aiFailureKind                                               *string
	aiAttempts                                                  int
	aiExtractionAt                                              *time.Time
	incidentID                                                  *string
	incidentAttachedAt                                          *time.Time
	alertID                                                     *string
	alertCreatedAt                                              *time.Time
	deliveryID, deliveryState                                   *string
	deliveryCreatedAt, deliveryLastAttemptAt, deliveredAt       *time.Time
	deliveryAttempts                                            int
	attemptState, attemptFailureKind                            *string
	attemptStartedAt, attemptCompletedAt, firstAttemptStartedAt *time.Time
}

func (s *Store) query(ctx context.Context, reportID *uuid.UUID, limit int) ([]Timeline, error) {
	rows, err := s.pool.Query(ctx, timelineQuery, reportID, limit)
	if err != nil {
		return nil, fmt.Errorf("query report latency timelines: %w", err)
	}
	defer rows.Close()
	result := make([]Timeline, 0, limit)
	now := time.Now().UTC()
	for rows.Next() {
		var row timelineRow
		if err := rows.Scan(
			&row.reportID, &row.submittedAt, &row.reportCreatedAt, &row.linkedIncidentID,
			&row.outboxCreatedAt, &row.outboxPublishedAt, &row.outboxRetries, &row.outboxHasError,
			&row.aiState, &row.aiStartedAt, &row.aiCompletedAt, &row.aiFailedAt, &row.aiFailureKind, &row.aiAttempts,
			&row.aiExtractionAt, &row.incidentID, &row.incidentAttachedAt,
			&row.alertID, &row.alertCreatedAt,
			&row.deliveryID, &row.deliveryState, &row.deliveryCreatedAt, &row.deliveryLastAttemptAt,
			&row.deliveredAt, &row.deliveryAttempts, &row.attemptState, &row.attemptFailureKind,
			&row.attemptStartedAt, &row.attemptCompletedAt, &row.firstAttemptStartedAt,
		); err != nil {
			return nil, fmt.Errorf("scan report latency timeline: %w", err)
		}
		result = append(result, row.timeline(now))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate report latency timelines: %w", err)
	}
	return result, nil
}

func (row timelineRow) timeline(now time.Time) Timeline {
	incidentID := row.incidentID
	if incidentID == nil {
		incidentID = row.linkedIncidentID
	}
	var outbox Observation
	if row.outboxCreatedAt != nil {
		outbox.StartedAt = row.outboxCreatedAt
		outbox.Attempts = row.outboxRetries
		switch {
		case row.outboxPublishedAt != nil:
			outbox.State, outbox.CompletedAt = StateCompleted, row.outboxPublishedAt
		case row.outboxHasError || row.outboxRetries > 0:
			outbox.State, outbox.FailureKind = StateFailedRetryable, "publish_retryable"
			outbox.LastActivityAt = row.outboxCreatedAt
		default:
			outbox.State, outbox.LastActivityAt = StatePending, row.outboxCreatedAt
		}
	} else {
		outbox.State = StateUnavailable
	}

	ai := Observation{State: StateUnavailable}
	if row.aiState != nil {
		ai.Attempts = row.aiAttempts
		switch *row.aiState {
		case "SUCCEEDED":
			ai.State, ai.StartedAt, ai.CompletedAt = StateCompleted, row.aiStartedAt, row.aiCompletedAt
		case "RUNNING":
			ai.State, ai.StartedAt, ai.LastActivityAt = StatePending, row.aiStartedAt, row.aiStartedAt
		case "FAILED_RETRYABLE":
			ai.State, ai.StartedAt, ai.CompletedAt = StateFailedRetryable, row.aiStartedAt, row.aiFailedAt
			ai.FailureKind = safeFailureKind(valueOf(row.aiFailureKind))
		case "FAILED_TERMINAL":
			ai.State, ai.StartedAt, ai.CompletedAt = StateFailedTerminal, row.aiStartedAt, row.aiFailedAt
			ai.FailureKind = safeFailureKind(valueOf(row.aiFailureKind))
		}
	} else if row.aiExtractionAt != nil {
		// Existing extraction rows prove completion; absent processing timestamps
		// remain unavailable rather than becoming a fabricated zero duration.
		ai.State, ai.CompletedAt = StateCompleted, row.aiExtractionAt
	} else if row.outboxPublishedAt != nil {
		ai.State, ai.StartedAt, ai.LastActivityAt = StatePending, row.outboxPublishedAt, row.outboxPublishedAt
	}

	incident := Observation{State: StateUnavailable}
	if row.incidentAttachedAt != nil {
		incident.State, incident.StartedAt, incident.CompletedAt = StateCompleted, row.aiCompletedAt, row.incidentAttachedAt
	} else if incidentID != nil && row.aiCompletedAt != nil {
		incident.State, incident.StartedAt, incident.LastActivityAt = StatePending, row.aiCompletedAt, row.aiCompletedAt
	}

	priority := Observation{State: StateUnavailable}
	alert := Observation{State: StateUnavailable}
	if row.alertCreatedAt != nil {
		alert.State, alert.CompletedAt = StateCompleted, row.alertCreatedAt
	} else if row.incidentAttachedAt != nil {
		alert.State, alert.StartedAt, alert.LastActivityAt = StatePending, row.incidentAttachedAt, row.incidentAttachedAt
	}

	delivery := DeliveryObservation{Observation: Observation{State: StateUnavailable}}
	if row.deliveryID != nil {
		delivery.StartedAt = row.deliveryCreatedAt
		delivery.AttemptStartedAt = row.firstAttemptStartedAt
		delivery.AttemptCompletedAt = row.attemptCompletedAt
		delivery.DeliveredAt = row.deliveredAt
		delivery.LastActivityAt = row.deliveryLastAttemptAt
		delivery.Attempts = row.deliveryAttempts
		delivery.QueueState = StatePending
		if row.firstAttemptStartedAt != nil {
			delivery.QueueState = StateCompleted
		}
		if row.attemptState == nil {
			if valueOf(row.deliveryState) == "IN_FLIGHT" {
				delivery.AttemptState = StatePending
			} else {
				delivery.AttemptState = StateUnavailable
			}
		} else {
			delivery.AttemptState = deliveryAttemptState(valueOf(row.deliveryState), valueOf(row.attemptState))
		}
		if delivery.AttemptState == StateFailedRetryable || delivery.AttemptState == StateFailedTerminal {
			delivery.FailureKind = safeFailureKind(valueOf(row.attemptFailureKind))
		}
		if delivery.AttemptState == StateCompleted && row.deliveredAt != nil {
			delivery.AttemptCompletedAt = row.deliveredAt
		}
	} else if row.alertCreatedAt != nil {
		delivery.QueueState = StatePending
		delivery.StartedAt = row.alertCreatedAt
		delivery.LastActivityAt = row.alertCreatedAt
	}

	return BuildTimeline(Input{
		ReportID: row.reportID, IncidentID: incidentID, AlertID: row.alertID, DeliveryID: row.deliveryID,
		Now: now, ReportSubmittedAt: &row.submittedAt, ReportPersistedAt: &row.reportCreatedAt,
		Outbox: outbox, AI: ai, Incident: incident, Priority: priority, Alert: alert, Delivery: delivery,
	})
}

func deliveryAttemptState(deliveryState, attemptState string) StageState {
	switch {
	case deliveryState == "QUARANTINED", attemptState == "QUARANTINED":
		return StateFailedTerminal
	case attemptState == "FAILED" || attemptState == "STALE":
		return StateFailedRetryable
	case attemptState == "STARTED" || deliveryState == "IN_FLIGHT":
		return StatePending
	case attemptState == "SUCCEEDED" || attemptState == "SKIPPED" || attemptState == "STALE" && deliveryState == "SUCCEEDED":
		return StateCompleted
	case deliveryState == "SUCCEEDED" || deliveryState == "SKIPPED":
		return StateCompleted
	default:
		return StateUnavailable
	}
}

func valueOf(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
