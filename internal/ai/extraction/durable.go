package extraction

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DurableStore persists only schema-validated extraction values. The unique
// report/contract key makes redelivery and concurrent delivery idempotent.
type DurableStore struct {
	pool      *pgxpool.Pool
	validator ExtractionValidator
}

func NewDurableStore(pool *pgxpool.Pool, validator ExtractionValidator) *DurableStore {
	return &DurableStore{pool: pool, validator: validator}
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
	var extraction Extraction
	if err := json.Unmarshal(result, &extraction); err != nil {
		return Extraction{}, false, fmt.Errorf("decode durable extraction: %w", err)
	}
	if _, err := s.validate(extraction); err != nil {
		return Extraction{}, false, fmt.Errorf("validate durable extraction: %w", err)
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
