package reports

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/observability"
)

var ErrIdempotencyConflict = errors.New("idempotency key was reused with a different request")

type DeviceLocation struct {
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Accuracy  *float64 `json:"accuracy,omitempty"`
}

type IngestRequest struct {
	RawText        string          `json:"raw_text"`
	DeviceLocation *DeviceLocation `json:"device_location,omitempty"`
}

type Acknowledgement struct {
	ReportID    string
	SubmittedAt string
}

type Ingestor interface {
	Ingest(context.Context, string, IngestRequest) (Acknowledgement, error)
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ingest(ctx context.Context, idempotencyKey string, input IngestRequest) (ack Acknowledgement, err error) {
	ctx, finish := observability.StartStage(ctx, observability.StageReportPersist)
	defer func() { finish(err) }()
	fingerprint, err := requestFingerprint(input)
	if err != nil {
		return Acknowledgement{}, fmt.Errorf("fingerprint report request: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Acknowledgement{}, fmt.Errorf("begin report transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var acknowledgement Acknowledgement
	var submittedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO reports (
			raw_text, device_location, location_accuracy, idempotency_key, request_fingerprint
		)
		VALUES (
			$1,
			CASE
				WHEN $2::double precision IS NULL OR $3::double precision IS NULL THEN NULL
				ELSE ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography
			END,
			$4, $5, $6
		)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING id::text, submitted_at
	`, input.RawText, locationLatitude(input.DeviceLocation), locationLongitude(input.DeviceLocation), locationAccuracy(input.DeviceLocation), idempotencyKey, fingerprint).Scan(
		&acknowledgement.ReportID,
		&submittedAt,
	)
	if err == nil {
		acknowledgement.SubmittedAt = submittedAt.UTC().Format(time.RFC3339Nano)
		payload, marshalErr := json.Marshal(struct {
			ReportID string `json:"report_id"`
		}{ReportID: acknowledgement.ReportID})
		if marshalErr != nil {
			return Acknowledgement{}, fmt.Errorf("marshal report event: %w", marshalErr)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload)
			VALUES ('report.created', 'report', $1, $2::jsonb)
		`, acknowledgement.ReportID, payload); err != nil {
			return Acknowledgement{}, fmt.Errorf("insert report outbox event: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Acknowledgement{}, fmt.Errorf("commit report transaction: %w", err)
		}
		return acknowledgement, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Acknowledgement{}, fmt.Errorf("insert report: %w", err)
	}

	var existingFingerprint *string
	var existingSubmittedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT id::text, submitted_at, request_fingerprint
		FROM reports
		WHERE idempotency_key = $1
	`, idempotencyKey).Scan(&acknowledgement.ReportID, &existingSubmittedAt, &existingFingerprint); err != nil {
		return Acknowledgement{}, fmt.Errorf("load existing idempotent report: %w", err)
	}
	acknowledgement.SubmittedAt = existingSubmittedAt.UTC().Format(time.RFC3339Nano)
	if existingFingerprint == nil || *existingFingerprint != fingerprint {
		return Acknowledgement{}, ErrIdempotencyConflict
	}
	return acknowledgement, nil
}

func requestFingerprint(input IngestRequest) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func locationLatitude(location *DeviceLocation) *float64 {
	if location == nil {
		return nil
	}
	return &location.Latitude
}

func locationLongitude(location *DeviceLocation) *float64 {
	if location == nil {
		return nil
	}
	return &location.Longitude
}

func locationAccuracy(location *DeviceLocation) *float64 {
	if location == nil {
		return nil
	}
	return location.Accuracy
}
