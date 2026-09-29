package verification

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

type postgresTargetingRepository struct{ pool *pgxpool.Pool }

func (r *postgresTargetingRepository) TargetIncident(ctx context.Context, incidentID uuid.UUID, targetPolicy config.VerificationTargetingPolicy, publicPolicy config.PublicIncidentGeometryPolicy) (int, error) {
	if r == nil || r.pool == nil {
		return 0, fmt.Errorf("verification targeting database is required")
	}
	if err := targetPolicy.Validate(); err != nil {
		return 0, fmt.Errorf("invalid verification targeting policy")
	}
	if err := publicPolicy.Validate(); err != nil {
		return 0, fmt.Errorf("invalid verification public projection policy")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin verifier targeting: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var status string
	var incidentExpiresAt *time.Time
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT status, expires_at, clock_timestamp() FROM incidents WHERE id=$1 FOR UPDATE`, incidentID).Scan(&status, &incidentExpiresAt, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("lock incident for verifier targeting: %w", err)
	}
	if (status != "OPEN" && status != "RESOLVING") || (incidentExpiresAt != nil && !incidentExpiresAt.After(now)) {
		return 0, nil
	}
	publicIncident, err := incidents.GetPublicIncidentWithQuerier(ctx, tx, incidentID, publicPolicy)
	if errors.Is(err, incidents.ErrPublicIncidentNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read public incident projection for verifier targeting: %w", err)
	}
	if publicIncident.ID != incidentID {
		return 0, nil
	}

	fingerprint := targetingPolicyFingerprint(targetPolicy)
	var existing int
	if err := tx.QueryRow(ctx, `
		SELECT GREATEST(
			(SELECT count(*) FROM verification_requests WHERE incident_id=$1 AND assigned_verifier_id IS NOT NULL AND targeting_policy_version=$2 AND targeting_policy_fingerprint=$3),
			(SELECT count(*) FROM verification_requests WHERE incident_id=$1 AND assigned_verifier_id IS NOT NULL AND cancelled_at IS NULL AND expires_at > $4)
		)
	`, incidentID, targetPolicy.Version, fingerprint[:], now).Scan(&existing); err != nil {
		return 0, fmt.Errorf("count existing verifier targeting requests: %w", err)
	}
	remaining := targetPolicy.MaxCandidates - existing
	if remaining <= 0 {
		return 0, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT g.user_id
		FROM user_locations AS ul
		JOIN user_capability_grants AS g
		  ON g.user_id=ul.user_id
		 AND g.capability='trusted_verifier'
		 AND g.revoked_at IS NULL
		CROSS JOIN LATERAL (
			SELECT CASE
				WHEN i.affected_geometry IS NOT NULL
				 AND NOT ST_IsEmpty(ST_CollectionExtract(i.affected_geometry::geometry, 3))
				THEN ST_Multi(ST_CollectionExtract(i.affected_geometry::geometry, 3))::geography
				ELSE i.center_point
			END AS point
			FROM incidents AS i
			WHERE i.id=$1
		) AS target
		WHERE ul.observed_at >= $2
		  AND ul.observed_at <= $3
		  AND ST_DWithin(ul.location, target.point, $4)
		  AND NOT EXISTS (
			SELECT 1 FROM verification_requests AS vr
			WHERE vr.incident_id=$1 AND vr.assigned_verifier_id=g.user_id
			  AND vr.cancelled_at IS NULL AND vr.expires_at > $3
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM verification_requests AS vr
			WHERE vr.incident_id=$1 AND vr.assigned_verifier_id=g.user_id
			  AND vr.targeting_policy_version=$5 AND vr.targeting_policy_fingerprint=$6
		  )
		ORDER BY ST_Distance(ul.location, target.point), g.user_id
		LIMIT $7
		FOR SHARE OF g, ul
	`, incidentID, now.Add(-targetPolicy.LocationMaxAge), now, targetPolicy.RadiusMeters, targetPolicy.Version, fingerprint[:], remaining)
	if err != nil {
		return 0, fmt.Errorf("select eligible verifier candidates: %w", err)
	}
	verifiers := make([]uuid.UUID, 0, remaining)
	for rows.Next() {
		var verifierID uuid.UUID
		if err := rows.Scan(&verifierID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan eligible verifier candidate: %w", err)
		}
		verifiers = append(verifiers, verifierID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate eligible verifier candidates: %w", err)
	}
	rows.Close()

	expiresAt := now.Add(targetPolicy.RequestLifetime)
	if incidentExpiresAt != nil && incidentExpiresAt.Before(expiresAt) {
		expiresAt = *incidentExpiresAt
	}
	created := 0
	for _, verifierID := range verifiers {
		tag, err := tx.Exec(ctx, `
			INSERT INTO verification_requests (incident_id, assigned_verifier_id, created_at, expires_at, targeting_policy_version, targeting_policy_fingerprint)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (incident_id, assigned_verifier_id, targeting_policy_version, targeting_policy_fingerprint)
			WHERE assigned_verifier_id IS NOT NULL
			  AND targeting_policy_version IS NOT NULL
			  AND targeting_policy_fingerprint IS NOT NULL DO NOTHING
		`, incidentID, verifierID, now, expiresAt, targetPolicy.Version, fingerprint[:])
		if err != nil {
			return 0, fmt.Errorf("insert targeted verification request: %w", err)
		}
		created += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit verifier targeting: %w", err)
	}
	return created, nil
}

func targetingPolicyFingerprint(policy config.VerificationTargetingPolicy) [32]byte {
	encoded := make([]byte, 32+len(policy.Version))
	binary.BigEndian.PutUint64(encoded[0:8], math.Float64bits(policy.RadiusMeters))
	binary.BigEndian.PutUint64(encoded[8:16], uint64(policy.LocationMaxAge))
	binary.BigEndian.PutUint64(encoded[16:24], uint64(policy.MaxCandidates))
	binary.BigEndian.PutUint64(encoded[24:32], uint64(policy.RequestLifetime))
	copy(encoded[32:], policy.Version)
	return sha256.Sum256(encoded)
}

var _ targetingRepository = (*postgresTargetingRepository)(nil)
