package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresConfig struct {
	MaxAttempts int
	Backoff     time.Duration
	Lease       time.Duration
}

func (c PostgresConfig) validate() error {
	if c.MaxAttempts < 1 || c.Backoff < 0 || c.Lease <= 0 {
		return fmt.Errorf("delivery retry configuration is invalid")
	}
	return nil
}

type PostgresStore struct {
	pool   *pgxpool.Pool
	config PostgresConfig
}

func NewPostgresStore(pool *pgxpool.Pool, config PostgresConfig) (*PostgresStore, error) {
	if pool == nil {
		return nil, errors.New("delivery database is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &PostgresStore{pool: pool, config: config}, nil
}

func (s *PostgresStore) StartAttempt(ctx context.Context, request Request, now time.Time) (StartResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StartResult{}, fmt.Errorf("begin delivery attempt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state State
	var storedAlertID, storedKey, channel, priority string
	var payload []byte
	var attempts int
	var activeAttemptNo *int
	var lastAttempt, nextAttempt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT d.state, d.alert_id::text, d.idempotency_key, d.channel, d.priority, d.payload, d.attempts, d.active_attempt_no, d.last_attempt_at, d.next_attempt_at
		FROM deliveries d WHERE d.id = $1 FOR UPDATE`, request.DeliveryID).
		Scan(&state, &storedAlertID, &storedKey, &channel, &priority, &payload, &attempts, &activeAttemptNo, &lastAttempt, &nextAttempt)
	if err == pgx.ErrNoRows {
		return StartResult{}, fmt.Errorf("delivery %s not found", request.DeliveryID)
	}
	if err != nil {
		return StartResult{}, fmt.Errorf("load delivery %s: %w", request.DeliveryID, err)
	}
	if storedAlertID != request.AlertID || storedKey != request.IdempotencyKey {
		return StartResult{}, fmt.Errorf("delivery %s identity conflict", request.DeliveryID)
	}
	if state == StateSucceeded || state == StateQuarantined || state == StateSkipped {
		return StartResult{Terminal: true}, nil
	}
	if state == StateInFlight && activeAttemptNo != nil && lastAttempt != nil && now.Sub(*lastAttempt) < s.config.Lease {
		return StartResult{NotDue: true, RetryAfter: s.config.Lease - now.Sub(*lastAttempt)}, nil
	}
	if attempts >= s.config.MaxAttempts {
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET state = 'QUARANTINED', updated_at = $2 WHERE id = $1`, request.DeliveryID, now); err != nil {
			return StartResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_quarantine (delivery_id, reason) VALUES ($1, $2) ON CONFLICT (delivery_id) DO NOTHING`, request.DeliveryID, "maximum delivery attempts exhausted"); err != nil {
			return StartResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return StartResult{}, fmt.Errorf("commit exhausted delivery: %w", err)
		}
		return StartResult{Terminal: true}, nil
	}
	if nextAttempt != nil && nextAttempt.After(now) {
		return StartResult{NotDue: true, RetryAfter: nextAttempt.Sub(now)}, nil
	}
	var superseded bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alerts WHERE supersedes_alert_id = $1)`, request.AlertID).Scan(&superseded); err != nil {
		return StartResult{}, fmt.Errorf("check alert supersession: %w", err)
	}
	if superseded {
		attemptNo := attempts + 1
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET state = 'SKIPPED', attempts = $2, active_attempt_no = NULL, updated_at = $3 WHERE id = $1`, request.DeliveryID, attemptNo, now); err != nil {
			return StartResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (delivery_id, attempt_no, operation_key, state, error_message, started_at, completed_at) VALUES ($1, $2, $3, 'SKIPPED', $4, $5, $5)`, request.DeliveryID, attemptNo, operationKey(request.IdempotencyKey), "alert superseded", now); err != nil {
			return StartResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return StartResult{}, fmt.Errorf("commit skipped delivery: %w", err)
		}
		return StartResult{Terminal: true}, nil
	}
	attemptNo := attempts + 1
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET state = 'IN_FLIGHT', attempts = $2, active_attempt_no = $2, last_attempt_at = $3, next_attempt_at = NULL, updated_at = $3 WHERE id = $1`, request.DeliveryID, attemptNo, now); err != nil {
		return StartResult{}, fmt.Errorf("claim delivery %s: %w", request.DeliveryID, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (delivery_id, attempt_no, operation_key, state, started_at) VALUES ($1, $2, $3, 'STARTED', $4)`, request.DeliveryID, attemptNo, operationKey(request.IdempotencyKey), now); err != nil {
		return StartResult{}, fmt.Errorf("record delivery attempt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return StartResult{}, fmt.Errorf("commit delivery attempt: %w", err)
	}
	return StartResult{Attempt: Attempt{DeliveryID: request.DeliveryID, AlertID: request.AlertID, Channel: channel, Priority: priority, Payload: json.RawMessage(payload), IdempotencyKey: request.IdempotencyKey, Attempt: attemptNo, OperationKey: operationKey(request.IdempotencyKey)}}, nil
}

func (s *PostgresStore) FinishAttempt(ctx context.Context, attempt Attempt, provider ProviderResult, kind FailureKind, deliveryErr error, now time.Time) (FinishResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return FinishResult{}, fmt.Errorf("begin delivery result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current State
	var attempts int
	var activeAttemptNo *int
	if err := tx.QueryRow(ctx, `SELECT state, attempts, active_attempt_no FROM deliveries WHERE id = $1 FOR UPDATE`, attempt.DeliveryID).Scan(&current, &attempts, &activeAttemptNo); err != nil {
		return FinishResult{}, fmt.Errorf("load delivery result state: %w", err)
	}
	if activeAttemptNo == nil || *activeAttemptNo != attempt.Attempt {
		if _, err := tx.Exec(ctx, `UPDATE delivery_attempts SET state = 'STALE', failure_kind = $3, error_message = $4, provider_response = $5, completed_at = $6 WHERE delivery_id = $1 AND attempt_no = $2 AND state = 'STARTED'`, attempt.DeliveryID, attempt.Attempt, string(kind), truncateErrorValue(deliveryErr), truncate(provider.Response), now); err != nil {
			return FinishResult{}, fmt.Errorf("record stale delivery attempt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return FinishResult{}, fmt.Errorf("commit stale delivery attempt: %w", err)
		}
		return FinishResult{State: current, Stale: true}, nil
	}
	if current == StateSucceeded || current == StateQuarantined || current == StateSkipped {
		return FinishResult{State: current, Terminal: true}, nil
	}
	errText := ""
	if deliveryErr != nil {
		errText = truncate(deliveryErr.Error())
	}
	if kind == "" {
		if _, err := tx.Exec(ctx, `UPDATE delivery_attempts SET state = 'SUCCEEDED', provider_response = $3, completed_at = $4 WHERE delivery_id = $1 AND attempt_no = $2`, attempt.DeliveryID, attempt.Attempt, truncate(provider.Response), now); err != nil {
			return FinishResult{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET state = 'SUCCEEDED', active_attempt_no = NULL, delivered_at = $2, provider_response = $3, last_error = NULL, updated_at = $2 WHERE id = $1 AND active_attempt_no = $4`, attempt.DeliveryID, now, truncate(provider.Response), attempt.Attempt); err != nil {
			return FinishResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return FinishResult{}, fmt.Errorf("commit successful delivery: %w", err)
		}
		return FinishResult{State: StateSucceeded, Terminal: true}, nil
	}
	terminal := kind == FailurePermanent || attempts >= s.config.MaxAttempts
	state := StatePending
	attemptState := "FAILED"
	if terminal {
		state, attemptState = StateQuarantined, "QUARANTINED"
	}
	if _, err := tx.Exec(ctx, `UPDATE delivery_attempts SET state = $3, failure_kind = $4, error_message = $5, provider_response = $6, completed_at = $7 WHERE delivery_id = $1 AND attempt_no = $2`, attempt.DeliveryID, attempt.Attempt, attemptState, string(kind), errText, truncate(provider.Response), now); err != nil {
		return FinishResult{}, err
	}
	var next *time.Time
	if !terminal {
		delay := s.config.Backoff * time.Duration(1<<min(attempt.Attempt-1, 20))
		value := now.Add(delay)
		next = &value
	}
	if _, err := tx.Exec(ctx, `UPDATE deliveries SET state = $2, active_attempt_no = NULL, next_attempt_at = $3, last_error = $4, provider_response = $5, updated_at = $6 WHERE id = $1 AND active_attempt_no = $7`, attempt.DeliveryID, state, next, errText, truncate(provider.Response), now, attempt.Attempt); err != nil {
		return FinishResult{}, err
	}
	if terminal {
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_quarantine (delivery_id, reason) VALUES ($1, $2) ON CONFLICT (delivery_id) DO UPDATE SET reason = EXCLUDED.reason, quarantined_at = now()`, attempt.DeliveryID, errText); err != nil {
			return FinishResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return FinishResult{}, fmt.Errorf("commit delivery failure: %w", err)
	}
	if terminal {
		return FinishResult{State: state, Terminal: true}, nil
	}
	return FinishResult{State: state, RetryAfter: s.config.Backoff * time.Duration(1<<min(attempt.Attempt-1, 20))}, nil
}

func operationKey(key string) string { return "signa:delivery:" + key }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func truncate(value string) string {
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

func truncateErrorValue(err error) string {
	if err == nil {
		return ""
	}
	return truncate(err.Error())
}
