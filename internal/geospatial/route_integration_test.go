//go:build integration

package geospatial

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/routing"
)

func TestIncidentRouteRelevanceUsesAffectedGeometryWithoutPersistingRoute(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	databaseURL := integrationDatabaseURL
	testURL, maintenance, databaseName := createIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runGoose(t, ctx, testURL, "up")

	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	incidentID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO incidents (id, status, confidence_state, severity, center_point, affected_geometry, started_at, created_at, updated_at)
		VALUES ($1, 'OPEN', 'EMERGING', 'HIGH',
		        ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography,
		        ST_GeomFromText('POLYGON((3.37 6.51, 3.39 6.51, 3.39 6.54, 3.37 6.54, 3.37 6.51))', 4326)::geography,
		        now(), now(), now())
	`, incidentID); err != nil {
		t.Fatal(err)
	}
	var beforeGeometry string
	if err := pool.QueryRow(ctx, `SELECT ST_AsText(affected_geometry::geometry) FROM incidents WHERE id = $1`, incidentID).Scan(&beforeGeometry); err != nil {
		t.Fatal(err)
	}

	intersecting := routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.36, 6.525}, {3.40, 6.525}}}
	if got, err := store.FindIncidentRouteRelevance(ctx, incidentID, intersecting); err != nil || got != RouteRelevanceRelevant {
		t.Fatalf("intersecting relevance = (%s, %v), want RELEVANT", got, err)
	}
	nonIntersecting := routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.36, 6.60}, {3.40, 6.60}}}
	if got, err := store.FindIncidentRouteRelevance(ctx, incidentID, nonIntersecting); err != nil || got != RouteRelevanceNotRelevant {
		t.Fatalf("non-intersecting relevance = (%s, %v), want NOT_RELEVANT", got, err)
	}
	encoded, err := json.Marshal(RouteRelevanceRelevant)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "3.36") || strings.Contains(string(encoded), "6.525") {
		t.Fatalf("route relevance exposed route coordinates: %s", encoded)
	}
	var afterGeometry string
	if err := pool.QueryRow(ctx, `SELECT ST_AsText(affected_geometry::geometry) FROM incidents WHERE id = $1`, incidentID).Scan(&afterGeometry); err != nil {
		t.Fatal(err)
	}
	if beforeGeometry != afterGeometry {
		t.Fatalf("affected geometry changed after route evaluation: before=%s after=%s", beforeGeometry, afterGeometry)
	}

	centerOnlyID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO incidents (id, status, confidence_state, severity, center_point, started_at, created_at, updated_at)
		VALUES ($1, 'OPEN', 'EMERGING', 'HIGH', ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography, now(), now(), now())
	`, centerOnlyID); err != nil {
		t.Fatal(err)
	}
	if got, err := store.FindIncidentRouteRelevance(ctx, centerOnlyID, intersecting); err != nil || got != RouteRelevanceUnknown {
		t.Fatalf("center-only relevance = (%s, %v), want UNKNOWN", got, err)
	}
	invalid := routing.GeoJSONLineString{Type: "Point", Coordinates: [][]float64{{3.36, 6.525}, {3.40, 6.525}}}
	if _, err := store.FindIncidentRouteRelevance(ctx, incidentID, invalid); !errors.Is(err, routing.ErrInvalidRequest) {
		t.Fatalf("invalid geometry error = %v, want routing.ErrInvalidRequest", err)
	}
}
