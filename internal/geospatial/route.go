package geospatial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onerandomd3v/signa/internal/routing"
)

type RouteRelevance string

const (
	RouteRelevanceUnknown     RouteRelevance = "UNKNOWN"
	RouteRelevanceNotRelevant RouteRelevance = "NOT_RELEVANT"
	RouteRelevanceRelevant    RouteRelevance = "RELEVANT"
)

// FindIncidentRouteRelevance evaluates a request-scoped route against the
// incident's private affected geometry. Center points alone intentionally do
// not imply route relevance, so incidents without affected_geometry return
// UNKNOWN rather than using an implicit corridor.
func (s *Store) FindIncidentRouteRelevance(ctx context.Context, incidentID uuid.UUID, geometry routing.GeoJSONLineString) (RouteRelevance, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("geospatial store dependencies are required")
	}
	if incidentID == uuid.Nil {
		return "", errors.New("incident id is required")
	}
	if err := geometry.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(geometry)
	if err != nil {
		return "", fmt.Errorf("encode route geometry: %w", err)
	}

	var relevance RouteRelevance
	err = s.pool.QueryRow(ctx, `
		SELECT CASE
			WHEN affected_geometry IS NULL THEN $3
			WHEN ST_Intersects(
				affected_geometry::geometry,
				ST_SetSRID(ST_GeomFromGeoJSON($2::json), 4326)
			) THEN $4
			ELSE $5
		END
		FROM incidents
		WHERE id = $1
	`, incidentID, string(encoded), RouteRelevanceUnknown, RouteRelevanceRelevant, RouteRelevanceNotRelevant).Scan(&relevance)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("incident %s was not found", incidentID)
	}
	if err != nil {
		return "", fmt.Errorf("find incident route relevance: %w", err)
	}
	return relevance, nil
}
