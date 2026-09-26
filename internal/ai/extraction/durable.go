package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProcessingStateRecorder records safe AI lifecycle state without retaining
// report content, provider responses, or raw errors.
type ProcessingStateRecorder interface {
	MarkAIProcessingStarted(context.Context, string, time.Time) error
	MarkAIProcessingSucceeded(context.Context, string, time.Time) error
	MarkAIProcessingFailed(context.Context, string, string, FailureKind, time.Time) error
}

// DurableStore persists only schema-validated extraction values. The unique
// report/contract key makes redelivery and concurrent delivery idempotent.
type DurableStore struct {
	pool      *pgxpool.Pool
	validator ExtractionValidator
}

func NewDurableStore(pool *pgxpool.Pool, validator ExtractionValidator) *DurableStore {
	return &DurableStore{pool: pool, validator: validator}
}

func (s *DurableStore) MarkAIProcessingStarted(ctx context.Context, reportID string, now time.Time) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("durable AI processing database is required")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO report_ai_processing (report_id, state, started_at, attempts, updated_at)
		VALUES ($1, 'RUNNING', $2, 1, $2)
		ON CONFLICT (report_id) DO UPDATE SET
			state = 'RUNNING', started_at = EXCLUDED.started_at,
			completed_at = NULL, last_failed_at = NULL, failure_kind = NULL,
			attempts = report_ai_processing.attempts + 1, updated_at = EXCLUDED.updated_at
	`, reportID, now.UTC())
	if err != nil {
		return fmt.Errorf("record AI processing start: %w", err)
	}
	return nil
}

func (s *DurableStore) MarkAIProcessingSucceeded(ctx context.Context, reportID string, now time.Time) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("durable AI processing database is required")
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE report_ai_processing
		SET state = 'SUCCEEDED', completed_at = $2, last_failed_at = NULL,
		    failure_kind = NULL, updated_at = $2
		WHERE report_id = $1
	`, reportID, now.UTC())
	if err != nil {
		return fmt.Errorf("record AI processing success: %w", err)
	}
	return nil
}

func (s *DurableStore) MarkAIProcessingFailed(ctx context.Context, reportID, state string, kind FailureKind, now time.Time) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("durable AI processing database is required")
	}
	if state != "FAILED_RETRYABLE" && state != "FAILED_TERMINAL" {
		return fmt.Errorf("invalid AI processing failure state %q", state)
	}
	if kind != FailureTransient && kind != FailurePermanent {
		return fmt.Errorf("invalid AI processing failure kind %q", kind)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO report_ai_processing (report_id, state, started_at, last_failed_at, failure_kind, attempts, updated_at)
		VALUES ($1, $2, $3, $3, $4, 1, $3)
		ON CONFLICT (report_id) DO UPDATE SET
			state = EXCLUDED.state, completed_at = NULL,
			last_failed_at = EXCLUDED.last_failed_at, failure_kind = EXCLUDED.failure_kind,
			updated_at = EXCLUDED.updated_at
	`, reportID, state, now.UTC(), string(kind))
	if err != nil {
		return fmt.Errorf("record AI processing failure: %w", err)
	}
	return nil
}

func (s *DurableStore) Find(ctx context.Context, reportID, contractVersion string) (Extraction, bool, error) {
	if s == nil || s.pool == nil {
		return Extraction{}, false, fmt.Errorf("durable extraction store database is required")
	}
	var result []byte
	err := s.pool.QueryRow(ctx, `
		SELECT result FROM report_ai_extractions
		WHERE report_id = $1 AND contract_version = $2
	`, reportID, contractVersion).Scan(&result)
	if err == pgx.ErrNoRows {
		return Extraction{}, false, nil
	}
	if err != nil {
		return Extraction{}, false, fmt.Errorf("load durable extraction: %w", err)
	}
	extraction, err := s.decodeStoredExtraction(result)
	if err != nil {
		return Extraction{}, false, err
	}
	return extraction, true, nil
}

func (s *DurableStore) Observe(ctx context.Context, reportID string, result Extraction) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("durable extraction store database is required")
	}
	encoded, err := s.validate(result)
	if err != nil {
		return fmt.Errorf("validate extraction before persistence: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin extraction transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var extractionID string
	err = tx.QueryRow(ctx, `
		INSERT INTO report_ai_extractions (report_id, contract_version, taxonomy_version, result)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (report_id, contract_version) DO NOTHING
		RETURNING id::text
	`, reportID, result.ContractVersion, result.TaxonomyVersion, encoded).Scan(&extractionID)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("persist validated extraction: %w", err)
	}
	payload, err := json.Marshal(struct {
		ReportID        string `json:"report_id"`
		ExtractionID    string `json:"extraction_id"`
		ContractVersion string `json:"contract_version"`
	}{reportID, extractionID, result.ContractVersion})
	if err != nil {
		return fmt.Errorf("encode extraction event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload)
		VALUES ('report.ai_processed', 'report', $1, $2::jsonb)
	`, reportID, payload); err != nil {
		return fmt.Errorf("insert extraction outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit extraction transaction: %w", err)
	}
	return nil
}

func (s *DurableStore) validate(result Extraction) ([]byte, error) {
	if s == nil || s.validator == nil {
		return nil, fmt.Errorf("durable extraction validator is required")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode extraction: %w", err)
	}
	if _, err := s.validator.Validate(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func (s *DurableStore) decodeStoredExtraction(data []byte) (Extraction, error) {
	if s == nil || s.validator == nil {
		return Extraction{}, fmt.Errorf("durable extraction validator is required")
	}
	// Validate the original JSON bytes before decoding into Extraction. This
	// preserves schema constraints such as additionalProperties: false that a
	// typed round-trip would otherwise erase.
	if _, err := s.validator.Validate(data); err != nil {
		return Extraction{}, fmt.Errorf("%w: validate stored extraction: %v", ErrInvalidStructuredOutput, err)
	}
	var extraction Extraction
	if err := json.Unmarshal(data, &extraction); err != nil {
		return Extraction{}, fmt.Errorf("%w: decode stored extraction: %v", ErrInvalidStructuredOutput, err)
	}
	return extraction, nil
}
