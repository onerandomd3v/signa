package priority

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/geospatial"
)

type Service struct {
	pool      *pgxpool.Pool
	proximity *geospatial.Store
	policy    Policy
}

type Evaluation struct {
	UserID   uuid.UUID
	Decision Decision
}

func NewService(pool *pgxpool.Pool, proximity *geospatial.Store, policy Policy) (*Service, error) {
	if pool == nil || proximity == nil {
		return nil, errors.New("priority service dependencies are required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Service{pool: pool, proximity: proximity, policy: policy}, nil
}

// EvaluateIncident loads the durable incident snapshot and evaluates every
// user returned by the privacy-safe COD-203 proximity query. Exact incident
// coordinates are used only inside this method and never cross the policy
// boundary or appear in its output.
func (s *Service) EvaluateIncident(ctx context.Context, incidentID uuid.UUID, asOf time.Time) ([]Evaluation, error) {
	if s == nil || s.pool == nil || s.proximity == nil {
		return nil, errors.New("priority service dependencies are required")
	}
	if incidentID == uuid.Nil || asOf.IsZero() {
		return nil, errors.New("incident id and as_of are required")
	}

	incident, target, err := s.loadIncident(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	proximity, err := s.proximity.FindUsersWithinRadius(ctx, geospatial.ProximityQuery{
		Target:       target,
		RadiusMeters: s.policy.P2RadiusMeters,
		AsOf:         asOf,
		MaxAge:       s.policy.LocationMaxAge,
	})
	if err != nil {
		return nil, fmt.Errorf("query priority proximity: %w", err)
	}

	evaluations := make([]Evaluation, 0, len(proximity))
	for _, result := range proximity {
		decision, err := Evaluate(s.policy, Input{
			Now:      asOf,
			Incident: incident,
			Proximity: Proximity{
				DistanceMeters: result.DistanceMeters,
				AccuracyMeters: result.AccuracyMeters,
				ObservedAt:     result.ObservedAt,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("evaluate priority for user %s: %w", result.UserID, err)
		}
		evaluations = append(evaluations, Evaluation{UserID: result.UserID, Decision: decision})
	}
	return evaluations, nil
}

func (s *Service) loadIncident(ctx context.Context, incidentID uuid.UUID) (Incident, geospatial.Point, error) {
	var incident Incident
	var target geospatial.Point
	var severity *string
	var activityAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT status, confidence_state, severity,
		       COALESCE(last_signal_at, started_at, created_at),
		       ST_Y(center_point::geometry), ST_X(center_point::geometry)
		FROM incidents
		WHERE id = $1 AND center_point IS NOT NULL
	`, incidentID).Scan(&incident.Status, &incident.Confidence, &severity, &activityAt, &target.Latitude, &target.Longitude)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, geospatial.Point{}, fmt.Errorf("incident %s was not found or has no center point", incidentID)
	}
	if err != nil {
		return Incident{}, geospatial.Point{}, fmt.Errorf("load incident %s: %w", incidentID, err)
	}
	if severity != nil {
		incident.Severity = *severity
	}
	if activityAt == nil {
		return Incident{}, geospatial.Point{}, fmt.Errorf("incident %s has no activity time", incidentID)
	}
	incident.ActivityAt = *activityAt
	return incident, target, nil
}
