package incidents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/incidents/lifecycle"
)

const (
	IncidentStatusChangedV1 = "incident.status_changed.v1"
	IncidentResolvedV1      = "incident.resolved.v1"
	IncidentStatusChanged   = "incident.status_changed"
	IncidentResolved        = "incident.resolved"
)

type LifecycleProcessor struct {
	pool   *pgxpool.Pool
	policy lifecycle.Policy
	now    func() time.Time
}

func NewLifecycleProcessor(pool *pgxpool.Pool, policy lifecycle.Policy) (*LifecycleProcessor, error) {
	if pool == nil {
		return nil, fmt.Errorf("lifecycle database is required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &LifecycleProcessor{pool: pool, policy: policy, now: time.Now}, nil
}

func (p *LifecycleProcessor) Process(ctx context.Context, message StreamMessage) error {
	if p == nil || p.pool == nil {
		return fmt.Errorf("lifecycle processor dependencies are required")
	}
	incidentID, err := parseLifecycleTrigger(message.Values)
	if err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin lifecycle evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := p.evaluateLocked(ctx, tx, incidentID, p.now().UTC()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit lifecycle evaluation: %w", err)
	}
	return nil
}

func (p *LifecycleProcessor) Sweep(ctx context.Context, limit int) error {
	if p == nil || p.pool == nil {
		return fmt.Errorf("lifecycle processor dependencies are required")
	}
	if limit <= 0 {
		return fmt.Errorf("lifecycle sweep limit must be positive")
	}
	now := p.now().UTC()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin lifecycle sweep: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT id
		FROM incidents
		WHERE (status = 'OPEN' AND COALESCE(last_signal_at, started_at, created_at) <= $1)
		   OR (status = 'RESOLVING' AND COALESCE(last_signal_at, started_at, created_at) <= $2)
		   OR (status = 'RESOLVED' AND (
				(expires_at IS NOT NULL AND expires_at <= $3)
				OR (expires_at IS NULL AND COALESCE(last_signal_at, started_at, created_at) <= $4)
		   ))
		ORDER BY COALESCE(expires_at, COALESCE(last_signal_at, started_at, created_at)), id
		LIMIT $5
		FOR UPDATE SKIP LOCKED
	`, now.Add(-p.policy.ResolvingAfter), now.Add(-p.policy.ResolvedAfter), now, now.Add(-p.policy.ExpiredAfter), limit)
	if err != nil {
		return fmt.Errorf("select lifecycle sweep batch: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan lifecycle sweep incident: %w", err)
		}
		ids = append(ids, id.String())
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate lifecycle sweep batch: %w", err)
	}
	rows.Close()
	for _, incidentID := range ids {
		if err := p.evaluateLocked(ctx, tx, incidentID, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit lifecycle sweep: %w", err)
	}
	return nil
}

func parseLifecycleTrigger(fields map[string]any) (string, error) {
	name, ok := streamString(fields, "event_name")
	if !ok || (name != IncidentCreatedV1 && name != IncidentReportAttachedV1) {
		return "", fmt.Errorf("unsupported lifecycle input event %q", name)
	}
	aggregate, ok := streamString(fields, "aggregate_type")
	if !ok || aggregate != "incident" {
		return "", fmt.Errorf("lifecycle input aggregate_type must be incident")
	}
	payload, ok := streamString(fields, "payload")
	if !ok {
		return "", fmt.Errorf("lifecycle input event missing payload")
	}
	var body struct {
		IncidentID string `json:"incident_id"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return "", fmt.Errorf("decode lifecycle input payload: %w", err)
	}
	if _, err := uuid.Parse(body.IncidentID); err != nil {
		return "", fmt.Errorf("invalid lifecycle incident_id: %w", err)
	}
	return body.IncidentID, nil
}

type lifecycleIncident struct {
	Status     string
	ActivityAt time.Time
	ExpiresAt  *time.Time
	ResolvedAt *time.Time
}

func (p *LifecycleProcessor) evaluateLocked(ctx context.Context, tx pgx.Tx, incidentID string, now time.Time) error {
	var incident lifecycleIncident
	if err := tx.QueryRow(ctx, `
		SELECT status, COALESCE(last_signal_at, started_at, created_at), expires_at, resolved_at
		FROM incidents WHERE id = $1 FOR UPDATE
	`, incidentID).Scan(&incident.Status, &incident.ActivityAt, &incident.ExpiresAt, &incident.ResolvedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return fmt.Errorf("lock incident %s for lifecycle evaluation: %w", incidentID, err)
	}
	decision, err := lifecycle.Evaluate(now, lifecycle.Incident{
		Status: incident.Status, ActivityAt: incident.ActivityAt, ExpiresAt: incident.ExpiresAt, ResolvedAt: incident.ResolvedAt,
	}, p.policy)
	if err != nil {
		return fmt.Errorf("evaluate incident %s lifecycle: %w", incidentID, err)
	}
	statusChanged := incident.Status != decision.Status
	expiresChanged := incident.ExpiresAt == nil || !incident.ExpiresAt.Equal(decision.ExpiresAt)
	resolvedChanged := !sameLifecycleTime(incident.ResolvedAt, decision.ResolvedAt)
	if !statusChanged && !expiresChanged && !resolvedChanged {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE incidents SET status = $2, expires_at = $3, resolved_at = $4, updated_at = $5 WHERE id = $1`, incidentID, decision.Status, decision.ExpiresAt, decision.ResolvedAt, now); err != nil {
		return fmt.Errorf("update incident %s lifecycle: %w", incidentID, err)
	}
	if !statusChanged {
		return nil
	}
	metadata, err := json.Marshal(map[string]any{
		"activity_at":             decision.ActivityAt,
		"age_seconds":             int64(decision.Age / time.Second),
		"resolving_after_seconds": int64(p.policy.ResolvingAfter / time.Second),
		"resolved_after_seconds":  int64(p.policy.ResolvedAfter / time.Second),
		"expired_after_seconds":   int64(p.policy.ExpiredAfter / time.Second),
	})
	if err != nil {
		return err
	}
	reason := lifecycleReason(decision.Status)
	if _, err := tx.Exec(ctx, `
		INSERT INTO incident_state_history (incident_id, changed_field, previous_value, new_value, reason, actor_type, rule_version, metadata)
		VALUES ($1, 'status', $2, $3, $4, 'system', $5, $6::jsonb)
	`, incidentID, incident.Status, decision.Status, reason, lifecycle.PolicyVersion, metadata); err != nil {
		return fmt.Errorf("write lifecycle history for incident %s: %w", incidentID, err)
	}
	effectiveAt := now
	if decision.Status == lifecycle.Resolved && decision.ResolvedAt != nil {
		effectiveAt = decision.ResolvedAt.UTC()
	}
	if decision.Status == lifecycle.Expired {
		effectiveAt = decision.ExpiresAt.UTC()
	}
	payload, err := json.Marshal(map[string]any{
		"incident_id": incidentID, "previous_status": incident.Status, "new_status": decision.Status,
		"policy_version": lifecycle.PolicyVersion, "effective_at": effectiveAt,
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload) VALUES ($1, 'incident', $2, $3::jsonb)`, IncidentStatusChanged, incidentID, payload); err != nil {
		return fmt.Errorf("write lifecycle status event for incident %s: %w", incidentID, err)
	}
	if decision.Status == lifecycle.Resolved {
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload) VALUES ($1, 'incident', $2, $3::jsonb)`, IncidentResolved, incidentID, payload); err != nil {
			return fmt.Errorf("write lifecycle resolved event for incident %s: %w", incidentID, err)
		}
	}
	return nil
}

func sameLifecycleTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func lifecycleReason(status string) string {
	switch status {
	case lifecycle.Open:
		return "fresh incident activity reopened lifecycle"
	case lifecycle.Resolving:
		return "incident activity reached resolving freshness threshold"
	case lifecycle.Resolved:
		return "incident activity reached resolved freshness threshold"
	case lifecycle.Expired:
		return "incident activity reached expiry freshness threshold"
	default:
		return "incident lifecycle policy transition"
	}
}
