package incidents

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidLookup = errors.New("invalid incident candidate lookup")

type CandidateLookup struct {
	EventType    string
	Latitude     float64
	Longitude    float64
	RadiusMeters float64
	AsOf         time.Time
	TimeWindow   time.Duration
	Limit        int
}

type Candidate struct {
	ID              uuid.UUID
	EventType       string
	Status          string
	ConfidenceState string
	Severity        *string
	DistanceMeters  float64
	StartedAt       *time.Time
	LastSignalAt    *time.Time
	ExpiresAt       *time.Time
	CreatedAt       time.Time
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) LookupCandidates(ctx context.Context, input CandidateLookup) ([]Candidate, error) {
	validated, err := validateCandidateLookup(input)
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT
			i.id,
			i.event_type,
			i.status,
			i.confidence_state,
			i.severity,
			ST_Distance(
				i.center_point,
				ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography
			) AS distance_meters,
			i.started_at,
			i.last_signal_at,
			i.expires_at,
			i.created_at
		FROM incidents AS i
		WHERE i.event_type = $1
		  AND i.center_point IS NOT NULL
		  AND i.resolved_at IS NULL
		  AND (i.expires_at IS NULL OR i.expires_at > $6)
		  AND COALESCE(i.last_signal_at, i.started_at, i.created_at) >= $6 - ($7 * INTERVAL '1 second')
		  AND COALESCE(i.last_signal_at, i.started_at, i.created_at) <= $6
		  AND ST_DWithin(
				i.center_point,
				ST_SetSRID(ST_MakePoint($3, $2), 4326)::geography,
				$4
		  )
		ORDER BY distance_meters ASC,
			COALESCE(i.last_signal_at, i.started_at, i.created_at) DESC,
			i.id ASC
		LIMIT $5
	`, validated.eventType, validated.latitude, validated.longitude, validated.radiusMeters, validated.limit, validated.asOf, validated.timeWindowSeconds)
	if err != nil {
		return nil, fmt.Errorf("query incident candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]Candidate, 0, validated.limit)
	for rows.Next() {
		var candidate Candidate
		if err := rows.Scan(
			&candidate.ID,
			&candidate.EventType,
			&candidate.Status,
			&candidate.ConfidenceState,
			&candidate.Severity,
			&candidate.DistanceMeters,
			&candidate.StartedAt,
			&candidate.LastSignalAt,
			&candidate.ExpiresAt,
			&candidate.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan incident candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident candidates: %w", err)
	}
	return candidates, nil
}

type validatedCandidateLookup struct {
	eventType         string
	latitude          float64
	longitude         float64
	radiusMeters      float64
	asOf              time.Time
	timeWindowSeconds float64
	limit             int
}

func validateCandidateLookup(input CandidateLookup) (validatedCandidateLookup, error) {
	eventType := strings.TrimSpace(input.EventType)
	if eventType == "" {
		return validatedCandidateLookup{}, fmt.Errorf("%w: event type is required", ErrInvalidLookup)
	}
	if !finite(input.Latitude) || input.Latitude < -90 || input.Latitude > 90 {
		return validatedCandidateLookup{}, fmt.Errorf("%w: latitude must be between -90 and 90", ErrInvalidLookup)
	}
	if !finite(input.Longitude) || input.Longitude < -180 || input.Longitude > 180 {
		return validatedCandidateLookup{}, fmt.Errorf("%w: longitude must be between -180 and 180", ErrInvalidLookup)
	}
	if !finite(input.RadiusMeters) || input.RadiusMeters <= 0 {
		return validatedCandidateLookup{}, fmt.Errorf("%w: radius must be finite and greater than zero", ErrInvalidLookup)
	}
	if input.AsOf.IsZero() {
		return validatedCandidateLookup{}, fmt.Errorf("%w: as_of is required", ErrInvalidLookup)
	}
	if input.TimeWindow <= 0 {
		return validatedCandidateLookup{}, fmt.Errorf("%w: time window must be greater than zero", ErrInvalidLookup)
	}
	if input.Limit < 1 || input.Limit > 100 {
		return validatedCandidateLookup{}, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidLookup)
	}
	timeWindowSeconds := input.TimeWindow.Seconds()
	if !finite(timeWindowSeconds) {
		return validatedCandidateLookup{}, fmt.Errorf("%w: time window is too large", ErrInvalidLookup)
	}
	return validatedCandidateLookup{
		eventType:         eventType,
		latitude:          input.Latitude,
		longitude:         input.Longitude,
		radiusMeters:      input.RadiusMeters,
		asOf:              input.AsOf,
		timeWindowSeconds: timeWindowSeconds,
		limit:             input.Limit,
	}, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
