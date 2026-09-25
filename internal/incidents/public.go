package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onerandomd3v/signa/internal/config"
)

const maxPublicIncidentLimit = 100

var ErrPublicIncidentNotFound = errors.New("public incident not found")

type PublicIncident struct {
	ID              uuid.UUID       `json:"id"`
	EventType       *string         `json:"event_type"`
	Status          string          `json:"status"`
	ConfidenceState string          `json:"confidence_state"`
	Severity        *string         `json:"severity"`
	PublicGeometry  json.RawMessage `json:"public_geometry"`
	StartedAt       *time.Time      `json:"started_at"`
	LastSignalAt    *time.Time      `json:"last_signal_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type PublicIncidentReader interface {
	ListPublicIncidents(context.Context, config.PublicIncidentGeometryPolicy) ([]PublicIncident, error)
	GetPublicIncident(context.Context, uuid.UUID, config.PublicIncidentGeometryPolicy) (PublicIncident, error)
}

const publicIncidentProjection = `
WITH generalized AS (
	SELECT
		i.id,
		i.event_type,
		i.status,
		i.confidence_state,
		i.severity,
		CASE
			WHEN i.affected_geometry IS NOT NULL
				AND NOT ST_IsEmpty(ST_CollectionExtract(i.affected_geometry::geometry, 3))
			THEN ST_Transform(
				ST_SimplifyPreserveTopology(
					ST_SnapToGrid(
						ST_Transform(ST_Multi(ST_CollectionExtract(i.affected_geometry::geometry, 3)), 3857),
						$1
					),
					$3
				),
				4326
			)
			WHEN i.center_point IS NOT NULL
			THEN ST_Transform(
				ST_Buffer(
					ST_SnapToGrid(ST_Transform(i.center_point::geometry, 3857), $1),
					$2
				),
				4326
			)
		END AS public_geometry,
		i.started_at,
		i.last_signal_at,
		i.updated_at
	FROM incidents AS i
	WHERE i.status IN ('OPEN', 'RESOLVING')
	  AND (i.center_point IS NOT NULL OR i.affected_geometry IS NOT NULL)
)
SELECT id, event_type, status, confidence_state, severity,
	ST_AsGeoJSON(public_geometry)::jsonb,
	started_at, last_signal_at, updated_at
FROM generalized
WHERE public_geometry IS NOT NULL
`

func (s *Store) ListPublicIncidents(ctx context.Context, policy config.PublicIncidentGeometryPolicy) ([]PublicIncident, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("incident store database is required")
	}
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid public incident geometry policy: %w", err)
	}
	rows, err := s.pool.Query(ctx, publicIncidentProjection+`
ORDER BY updated_at DESC, id ASC
LIMIT $4
`, policy.GridMeters, policy.MinRadiusMeters, policy.SimplifyMeters, maxPublicIncidentLimit)
	if err != nil {
		return nil, fmt.Errorf("query public incidents: %w", err)
	}
	defer rows.Close()

	incidents := make([]PublicIncident, 0, maxPublicIncidentLimit)
	for rows.Next() {
		incident, err := scanPublicIncident(rows)
		if err != nil {
			return nil, err
		}
		incidents = append(incidents, incident)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public incidents: %w", err)
	}
	return incidents, nil
}

func (s *Store) GetPublicIncident(ctx context.Context, incidentID uuid.UUID, policy config.PublicIncidentGeometryPolicy) (PublicIncident, error) {
	if s == nil || s.pool == nil {
		return PublicIncident{}, fmt.Errorf("incident store database is required")
	}
	if err := policy.Validate(); err != nil {
		return PublicIncident{}, fmt.Errorf("invalid public incident geometry policy: %w", err)
	}
	row := s.pool.QueryRow(ctx, publicIncidentProjection+` AND id = $4
`, policy.GridMeters, policy.MinRadiusMeters, policy.SimplifyMeters, incidentID)
	incident, err := scanPublicIncident(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicIncident{}, ErrPublicIncidentNotFound
	}
	if err != nil {
		return PublicIncident{}, err
	}
	return incident, nil
}

type publicIncidentScanner interface {
	Scan(...any) error
}

func scanPublicIncident(row publicIncidentScanner) (PublicIncident, error) {
	var incident PublicIncident
	var geometry []byte
	if err := row.Scan(
		&incident.ID,
		&incident.EventType,
		&incident.Status,
		&incident.ConfidenceState,
		&incident.Severity,
		&geometry,
		&incident.StartedAt,
		&incident.LastSignalAt,
		&incident.UpdatedAt,
	); err != nil {
		return PublicIncident{}, fmt.Errorf("scan public incident: %w", err)
	}
	incident.PublicGeometry = json.RawMessage(geometry)
	return incident, nil
}
