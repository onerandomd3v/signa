package verification

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresRepository struct{ pool *pgxpool.Pool }

func (r *postgresRepository) ActiveGrant(ctx context.Context, userID uuid.UUID) (bool, error) {
	if r.pool == nil {
		return false, ErrUnavailable
	}
	var active bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM user_capability_grants WHERE user_id=$1 AND capability='trusted_verifier' AND revoked_at IS NULL)`, userID).Scan(&active)
	return active, err
}

const eligibleRequestPredicate = `cancelled_at IS NULL AND expires_at > clock_timestamp() AND (assigned_verifier_id IS NULL OR assigned_verifier_id=$1)`

func (r *postgresRepository) ListEligible(ctx context.Context, userID uuid.UUID) ([]requestRecord, error) {
	if r.pool == nil {
		return nil, ErrUnavailable
	}
	rows, err := r.pool.Query(ctx, `SELECT id,incident_id,created_at,expires_at FROM verification_requests WHERE `+eligibleRequestPredicate+` ORDER BY created_at DESC, id DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]requestRecord, 0, maxRequestListLimit)
	for rows.Next() {
		var request requestRecord
		if err := rows.Scan(&request.ID, &request.IncidentID, &request.CreatedAt, &request.ExpiresAt); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}
func (r *postgresRepository) GetEligible(ctx context.Context, userID, requestID uuid.UUID) (requestRecord, error) {
	if r.pool == nil {
		return requestRecord{}, ErrUnavailable
	}
	var request requestRecord
	err := r.pool.QueryRow(ctx, `SELECT id,incident_id,created_at,expires_at FROM verification_requests WHERE `+eligibleRequestPredicate+` AND id=$2`, userID, requestID).Scan(&request.ID, &request.IncidentID, &request.CreatedAt, &request.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return requestRecord{}, ErrRequestNotFound
	}
	return request, err
}

func (r *postgresRepository) Submit(ctx context.Context, userID, requestID uuid.UUID, key string, input ResponseInput, version int16, digest [32]byte) (Response, bool, error) {
	if r.pool == nil {
		return Response{}, false, ErrUnavailable
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Response{}, false, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT revoked_at FROM user_capability_grants WHERE user_id=$1 AND capability='trusted_verifier' AND revoked_at IS NULL FOR UPDATE`, userID).Scan(&revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Response{}, false, ErrNotTrustedVerifier
	}
	if err != nil {
		return Response{}, false, err
	}
	if revokedAt != nil {
		return Response{}, false, ErrNotTrustedVerifier
	}

	var request requestRecord
	var assigned *uuid.UUID
	var cancelledAt *time.Time
	err = tx.QueryRow(ctx, `SELECT id,incident_id,created_at,expires_at,assigned_verifier_id,cancelled_at FROM verification_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(&request.ID, &request.IncidentID, &request.CreatedAt, &request.ExpiresAt, &assigned, &cancelledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Response{}, false, ErrRequestNotFound
	}
	if err != nil {
		return Response{}, false, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return Response{}, false, err
	}
	if cancelledAt != nil || !request.ExpiresAt.After(now) || (assigned != nil && *assigned != userID) {
		return Response{}, false, ErrRequestNotFound
	}

	conclusion := nullableConclusion(input.Conclusion)
	observation := nullableObservation(input.Observation)
	row := tx.QueryRow(ctx, `INSERT INTO verification_responses (request_id,incident_id,verifier_id,conclusion,observation,payload_fingerprint_version,payload_fingerprint,idempotency_key) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (request_id,verifier_id,idempotency_key) DO NOTHING RETURNING id,request_id,incident_id,conclusion,observation,created_at`, requestID, request.IncidentID, userID, conclusion, observation, version, digest[:], key)
	response, err := scanResponse(row)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return Response{}, false, err
		}
		return response, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Response{}, false, err
	}
	var storedVersion int16
	var storedDigest []byte
	row = tx.QueryRow(ctx, `SELECT id,request_id,incident_id,conclusion,observation,created_at,payload_fingerprint_version,payload_fingerprint FROM verification_responses WHERE request_id=$1 AND verifier_id=$2 AND idempotency_key=$3`, requestID, userID, key)
	response, err = scanResponseWithFingerprint(row, &storedVersion, &storedDigest)
	if err != nil {
		return Response{}, false, err
	}
	if storedVersion != version || !bytes.Equal(storedDigest, digest[:]) {
		return Response{}, false, ErrIdempotencyConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return Response{}, false, err
	}
	return response, false, nil
}

func nullableConclusion(value *Conclusion) any {
	if value == nil {
		return nil
	}
	return string(*value)
}
func nullableObservation(value *Observation) any {
	if value == nil {
		return nil
	}
	return string(*value)
}

type scanner interface{ Scan(...any) error }

func scanResponse(row scanner) (Response, error) { return scanResponseWithFingerprint(row, nil, nil) }
func scanResponseWithFingerprint(row scanner, version *int16, digest *[]byte) (Response, error) {
	var response Response
	var conclusion, observation *string
	fields := []any{&response.ID, &response.RequestID, &response.IncidentID, &conclusion, &observation, &response.CreatedAt}
	if version != nil && digest != nil {
		fields = append(fields, version, digest)
	}
	if err := row.Scan(fields...); err != nil {
		return Response{}, err
	}
	if conclusion != nil {
		value := Conclusion(*conclusion)
		response.Conclusion = &value
	}
	if observation != nil {
		value := Observation(*observation)
		response.Observation = &value
	}
	return response, nil
}

var _ repository = (*postgresRepository)(nil)
