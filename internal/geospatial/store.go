package geospatial

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("geospatial database is required")
	}
	return &Store{pool: pool}, nil
}

// UpsertUserLocation replaces the current snapshot only when the observation
// is newer. The boolean reports whether the snapshot was inserted or updated.
func (s *Store) UpsertUserLocation(ctx context.Context, location RestrictedUserLocation) (bool, error) {
	if s == nil || s.pool == nil {
		return false, errors.New("geospatial store dependencies are required")
	}
	if err := location.Validate(); err != nil {
		return false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin upsert user location: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockUserLocation(ctx, tx, location.UserID); err != nil {
		return false, err
	}
	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO user_locations (user_id, location, accuracy_meters, observed_at)
		SELECT $1, ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography, $4, $5
		WHERE NOT EXISTS (
			SELECT 1 FROM user_location_deletions
			WHERE user_id = $1 AND last_observed_at >= $5
		)
		ON CONFLICT (user_id) DO UPDATE
		SET location = EXCLUDED.location,
		    accuracy_meters = EXCLUDED.accuracy_meters,
		    observed_at = EXCLUDED.observed_at,
		    updated_at = now()
		WHERE EXCLUDED.observed_at > user_locations.observed_at
		  AND NOT EXISTS (
			SELECT 1 FROM user_location_deletions
			WHERE user_id = EXCLUDED.user_id AND last_observed_at >= EXCLUDED.observed_at
		  )
		RETURNING user_id::text
	`, location.UserID, location.Point.Latitude, location.Point.Longitude, location.AccuracyMeters, location.ObservedAt).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("upsert user location: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit upsert user location: %w", err)
	}
	return userID != "", nil
}

func (s *Store) DeleteUserLocation(ctx context.Context, userID uuid.UUID, deletedAt time.Time) error {
	if s == nil || s.pool == nil {
		return errors.New("geospatial store dependencies are required")
	}
	if userID == uuid.Nil {
		return fmt.Errorf("%w: user_id is required", ErrInvalidLocation)
	}
	if deletedAt.IsZero() {
		return fmt.Errorf("%w: deleted_at is required", ErrInvalidLocation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete user location: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockUserLocation(ctx, tx, userID); err != nil {
		return err
	}
	var currentObservedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT observed_at FROM user_locations WHERE user_id = $1 FOR UPDATE`, userID).Scan(&currentObservedAt); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read current user location: %w", err)
	}
	watermark := deletedAt
	if currentObservedAt != nil && currentObservedAt.After(watermark) {
		watermark = *currentObservedAt
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO user_location_deletions (user_id, last_observed_at, deleted_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE
		SET last_observed_at = GREATEST(user_location_deletions.last_observed_at, EXCLUDED.last_observed_at),
		    deleted_at = GREATEST(user_location_deletions.deleted_at, EXCLUDED.deleted_at),
		    updated_at = now()
	`, userID, watermark, deletedAt); err != nil {
		return fmt.Errorf("record user location deletion: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_locations WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete user location: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete user location: %w", err)
	}
	return nil
}

// FindUsersWithinRadius returns deterministic, privacy-safe proximity data for
// snapshots observed within the caller's explicit [AsOf-MaxAge, AsOf] window.
// It never returns the exact stored coordinates.
func (s *Store) FindUsersWithinRadius(ctx context.Context, query ProximityQuery) ([]ProximityResult, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("geospatial store dependencies are required")
	}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	queryString := `
		SELECT ul.user_id, ST_Distance(ul.location, target.point), ul.accuracy_meters, ul.observed_at
		FROM user_locations AS ul
		CROSS JOIN (
			SELECT ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography AS point
		) AS target
		WHERE ST_DWithin(ul.location, target.point, $3)
		  AND ul.observed_at >= $4
		  AND ul.observed_at <= $5
		ORDER BY ST_Distance(ul.location, target.point), ul.user_id
		LIMIT $6
	`
	effectiveLimit := query.Limit
	if effectiveLimit == 0 {
		effectiveLimit = maxProximityLimit
	}
	rows, err := s.pool.Query(ctx, queryString, query.Target.Latitude, query.Target.Longitude, query.RadiusMeters, query.AsOf.Add(-query.MaxAge), query.AsOf, effectiveLimit)
	if err != nil {
		return nil, fmt.Errorf("find users within radius: %w", err)
	}
	defer rows.Close()
	results := make([]ProximityResult, 0)
	for rows.Next() {
		var result ProximityResult
		if err := rows.Scan(&result.UserID, &result.DistanceMeters, &result.AccuracyMeters, &result.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan proximity result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proximity results: %w", err)
	}
	return results, nil
}

func lockUserLocation(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, userID.String()); err != nil {
		return fmt.Errorf("lock user location %s: %w", userID, err)
	}
	return nil
}
