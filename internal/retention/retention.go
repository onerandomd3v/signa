package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Policy contains product-approved retention durations supplied by deployment
// configuration. The package intentionally has no policy defaults.
type Policy struct {
	ReportExactLocation time.Duration
	UserLocation        time.Duration
	MediaMetadata       time.Duration
	// OperationalLogs applies only to safely expired, published outbox
	// transport records. Durable incident and delivery audit history is not
	// swept by this policy.
	OperationalLogs time.Duration
	AuthSessions    time.Duration
	SweepInterval   time.Duration
	BatchSize       int
}

func (p Policy) Validate() error {
	for name, value := range map[string]time.Duration{
		"report exact location": p.ReportExactLocation,
		"user location":         p.UserLocation,
		"media metadata":        p.MediaMetadata,
		"operational logs":      p.OperationalLogs,
		"auth sessions":         p.AuthSessions,
		"sweep interval":        p.SweepInterval,
	} {
		if value <= 0 {
			return fmt.Errorf("%s retention duration must be greater than zero", name)
		}
	}
	if p.BatchSize < 1 || p.BatchSize > 10_000 {
		return errors.New("retention batch size must be between 1 and 10000")
	}
	return nil
}

type Counts struct {
	ReportLocations int64
	UserLocations   int64
	MediaMetadata   int64
	OutboxEvents    int64
	AuthSessions    int64
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("retention database is required")
	}
	return &Store{pool: pool}, nil
}

// Sweep applies one bounded, repeatable retention pass. It never deletes
// reports, incidents, alerts, deliveries, or durable audit history, and it
// never deletes external media objects because the storage abstraction has no
// safe delete contract.
func (s *Store) Sweep(ctx context.Context, now time.Time, policy Policy) (Counts, error) {
	if s == nil || s.pool == nil {
		return Counts{}, errors.New("retention database is required")
	}
	if now.IsZero() {
		return Counts{}, errors.New("retention sweep time is required")
	}
	if err := policy.Validate(); err != nil {
		return Counts{}, err
	}

	var counts Counts
	var err error
	counts.ReportLocations, err = sweepBatches(ctx, policy.BatchSize, func(ctx context.Context) (int64, error) {
		return s.execBatch(ctx, `
			UPDATE reports
			SET claimed_location = NULL, device_location = NULL, location_accuracy = NULL
			WHERE id IN (
				SELECT id FROM reports
				WHERE (claimed_location IS NOT NULL OR device_location IS NOT NULL) AND created_at < $1
				ORDER BY created_at, id LIMIT $2
			)
		`, now.Add(-policy.ReportExactLocation), policy.BatchSize)
	})
	if err != nil {
		return counts, fmt.Errorf("minimize report locations: %w", err)
	}
	counts.UserLocations, err = sweepBatches(ctx, policy.BatchSize, func(ctx context.Context) (int64, error) {
		return s.execBatch(ctx, `
			WITH expired AS (
				SELECT user_id, observed_at
				FROM user_locations
				WHERE observed_at < $1
				ORDER BY observed_at, user_id LIMIT $2
				FOR UPDATE
			), watermarks AS (
				INSERT INTO user_location_deletions (user_id, last_observed_at, deleted_at)
				SELECT user_id, observed_at, $3 FROM expired
				ON CONFLICT (user_id) DO UPDATE SET
					last_observed_at = GREATEST(user_location_deletions.last_observed_at, EXCLUDED.last_observed_at),
					deleted_at = GREATEST(user_location_deletions.deleted_at, EXCLUDED.deleted_at),
					updated_at = now()
				RETURNING user_id
			)
			DELETE FROM user_locations AS locations
			USING watermarks
			WHERE locations.user_id = watermarks.user_id
			  AND locations.observed_at < $1
		`, now.Add(-policy.UserLocation), policy.BatchSize, now)
	})
	if err != nil {
		return counts, fmt.Errorf("delete user locations: %w", err)
	}
	counts.MediaMetadata, err = sweepBatches(ctx, policy.BatchSize, func(ctx context.Context) (int64, error) {
		return s.execBatch(ctx, `
			DELETE FROM report_media
			WHERE id IN (
				SELECT id FROM report_media
				WHERE created_at < $1
				ORDER BY created_at, id LIMIT $2
			)
		`, now.Add(-policy.MediaMetadata), policy.BatchSize)
	})
	if err != nil {
		return counts, fmt.Errorf("delete media metadata: %w", err)
	}

	operationalCutoff := now.Add(-policy.OperationalLogs)
	counts.OutboxEvents, err = sweepBatches(ctx, policy.BatchSize, func(ctx context.Context) (int64, error) {
		return s.execBatch(ctx, `
			DELETE FROM outbox_events
			WHERE id IN (
				SELECT id FROM outbox_events
				WHERE published_at IS NOT NULL AND created_at < $1
				ORDER BY created_at, id LIMIT $2
			)
		`, operationalCutoff, policy.BatchSize)
	})
	if err != nil {
		return counts, fmt.Errorf("delete published outbox events: %w", err)
	}
	counts.AuthSessions, err = sweepBatches(ctx, policy.BatchSize, func(ctx context.Context) (int64, error) {
		return s.execBatch(ctx, `
			DELETE FROM auth_sessions
			WHERE id IN (
				SELECT id FROM auth_sessions
				WHERE (revoked_at IS NOT NULL OR expires_at <= $1)
				  AND (CASE WHEN revoked_at IS NOT NULL THEN revoked_at ELSE expires_at END) < $2
				ORDER BY (CASE WHEN revoked_at IS NOT NULL THEN revoked_at ELSE expires_at END), id LIMIT $3
			)
		`, now, now.Add(-policy.AuthSessions), policy.BatchSize)
	})
	if err != nil {
		return counts, fmt.Errorf("delete auth sessions: %w", err)
	}
	return counts, nil
}

// RunSweep performs an immediate pass and then repeats at the configured
// interval until cancellation. Logs contain only aggregate counts.
func (s *Store) RunSweep(ctx context.Context, policy Policy, logger *slog.Logger) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	run := func() {
		counts, err := s.Sweep(ctx, time.Now().UTC(), policy)
		if err != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Error("retention sweep failed", "error", err)
			}
			return
		}
		if logger != nil {
			logger.Info("retention sweep completed",
				"report_locations", counts.ReportLocations,
				"user_locations", counts.UserLocations,
				"media_metadata", counts.MediaMetadata,
				"outbox_events", counts.OutboxEvents,
				"auth_sessions", counts.AuthSessions,
			)
		}
	}
	run()
	ticker := time.NewTicker(policy.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			run()
		}
	}
}

func (s *Store) execBatch(ctx context.Context, query string, args ...any) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func sweepBatches(ctx context.Context, batchSize int, batch func(context.Context) (int64, error)) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, err := batch(ctx)
		if err != nil {
			return total, err
		}
		total += count
		if count < int64(batchSize) {
			return total, nil
		}
	}
}
