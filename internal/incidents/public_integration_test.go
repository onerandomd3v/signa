//go:build integration

package incidents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/geospatial"
	"github.com/onerandomd3v/signa/internal/routing"
)

func TestPublicIncidentGeometryIntegration(t *testing.T) {
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = integrationDatabaseURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	testDatabaseURL, maintenance, databaseName := createIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runGoose(t, ctx, testDatabaseURL, "up")

	pool, err := pgxpool.New(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping PostgreSQL pool: %v", err)
	}

	policy := config.PublicIncidentGeometryPolicy{
		Version:         config.PublicIncidentGeometryPolicyVersion,
		GridMeters:      100,
		MinRadiusMeters: 250,
		SimplifyMeters:  25,
	}
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	centerID := uuid.New()
	affectedID := uuid.New()
	collapsedID := uuid.New()
	resolvedID := uuid.New()
	expiredID := uuid.New()
	emptyID := uuid.New()

	_, err = pool.Exec(ctx, `
		INSERT INTO incidents (id, event_type, status, confidence_state, severity, center_point, started_at, last_signal_at, created_at, updated_at)
		VALUES ($1, 'center_only', 'OPEN', 'UNVERIFIED', NULL, ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography, $2, $2, $2, $2)
	`, centerID, base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO incidents (id, event_type, status, confidence_state, severity, center_point, affected_geometry, started_at, last_signal_at, created_at, updated_at)
		VALUES ($1, 'collapsed_area', 'OPEN', 'UNVERIFIED', NULL,
			ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography,
			ST_GeomFromText('POLYGON((3.37919 6.52439, 3.37921 6.52439, 3.37921 6.52441, 3.37919 6.52441, 3.37919 6.52439))', 4326)::geography,
			$2, $2, $2, $2)
	`, collapsedID, base.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO incidents (id, event_type, status, confidence_state, severity, affected_geometry, started_at, last_signal_at, created_at, updated_at)
		VALUES ($1, 'affected_area', 'RESOLVING', 'EMERGING', 'MODERATE', ST_GeomFromText('POLYGON((3.3780 6.5235, 3.3804 6.5235, 3.3804 6.5253, 3.3780 6.5253, 3.3780 6.5235))', 4326)::geography, $2, $2, $2, $2)
	`, affectedID, base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id     uuid.UUID
		status string
	}{
		{id: resolvedID, status: "RESOLVED"},
		{id: expiredID, status: "EXPIRED"},
	} {
		_, err = pool.Exec(ctx, `
			INSERT INTO incidents (id, event_type, status, confidence_state, center_point, started_at, last_signal_at, created_at, updated_at)
			VALUES ($1, 'inactive', $2, 'UNVERIFIED', ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography, $3, $3, $3, $3)
		`, test.id, test.status, base)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO incidents (id, event_type, status, confidence_state, started_at, last_signal_at, created_at, updated_at)
		VALUES ($1, 'no_geometry', 'OPEN', 'UNVERIFIED', $2, $2, $2, $2)
	`, emptyID, base)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < maxPublicIncidentLimit+5; i++ {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO incidents (id, event_type, status, confidence_state, center_point, started_at, last_signal_at, created_at, updated_at)
			VALUES ($1, 'bounded', 'OPEN', 'UNVERIFIED', ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography, $2, $2, $2, $2)
		`, id, base.Add(time.Duration(i+2)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	store := NewStore(pool)
	route := routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.376, 6.5244}, {3.382, 6.5244}}}
	classification, routeIncidents, err := store.FindPublicRouteRelevance(ctx, route, policy)
	if err != nil {
		t.Fatal(err)
	}
	if classification != geospatial.RouteRelevanceRelevant {
		t.Fatalf("route classification = %q, want RELEVANT", classification)
	}
	if len(routeIncidents) == 0 {
		t.Fatal("route relevance returned no public incident references")
	}
	encodedRouteIncidents, err := json.Marshal(routeIncidents)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedRouteIncidents), "center_point") || strings.Contains(string(encodedRouteIncidents), "reporter") {
		t.Fatalf("route relevance exposed private incident fields: %s", encodedRouteIncidents)
	}
	items, err := store.ListPublicIncidents(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != maxPublicIncidentLimit {
		t.Fatalf("public incident count = %d, want bounded limit %d", len(items), maxPublicIncidentLimit)
	}

	truncatedRouteAffectedID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO incidents (id, event_type, status, confidence_state, affected_geometry, started_at, last_signal_at, created_at, updated_at)
		VALUES ($1, 'older_route_intersection', 'OPEN', 'EMERGING',
			ST_GeomFromText('POLYGON((10.000 10.000, 10.010 10.000, 10.010 10.010, 10.000 10.010, 10.000 10.000))', 4326)::geography,
			$2, $2, $2, $2)
	`, truncatedRouteAffectedID, base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxPublicIncidentLimit+5; i++ {
		id := uuid.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO incidents (id, event_type, status, confidence_state, center_point, started_at, last_signal_at, created_at, updated_at)
			VALUES ($1, 'newer_non_intersecting', 'OPEN', 'UNVERIFIED', ST_SetSRID(ST_MakePoint(20, 20), 4326)::geography, $2, $2, $2, $2)
		`, id, base.Add(time.Duration(i+10)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}

	truncatedRoute := routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{9.99, 10.005}, {10.02, 10.005}}}
	truncatedClassification, truncatedIncidents, err := store.FindPublicRouteRelevance(ctx, truncatedRoute, policy)
	if err != nil {
		t.Fatal(err)
	}
	if truncatedClassification != geospatial.RouteRelevanceRelevant {
		t.Fatalf("truncated route classification = %q, want RELEVANT", truncatedClassification)
	}
	if len(truncatedIncidents) != maxPublicIncidentLimit {
		t.Fatalf("truncated route incident count = %d, want bounded limit %d", len(truncatedIncidents), maxPublicIncidentLimit)
	}
	for _, incident := range truncatedIncidents {
		if incident.ID == truncatedRouteAffectedID {
			t.Fatalf("older intersecting incident %s was returned despite projection truncation", truncatedRouteAffectedID)
		}
	}
	for _, item := range items {
		if item.Status != "OPEN" && item.Status != "RESOLVING" {
			t.Fatalf("inactive incident returned: %+v", item)
		}
		if item.ID == resolvedID || item.ID == expiredID || item.ID == emptyID {
			t.Fatalf("unexpected incident returned: %s", item.ID)
		}
	}

	center, err := store.GetPublicIncident(ctx, centerID, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertPublicGeometry(t, center.PublicGeometry)
	centerJSON := string(center.PublicGeometry)
	if strings.Contains(centerJSON, "3.3792") || strings.Contains(centerJSON, "6.5244") {
		t.Fatalf("center-only public geometry retained exact center coordinates: %s", centerJSON)
	}
	centerAgain, err := store.GetPublicIncident(ctx, centerID, policy)
	if err != nil {
		t.Fatal(err)
	}
	if string(center.PublicGeometry) != string(centerAgain.PublicGeometry) {
		t.Fatal("identical center input produced non-deterministic public geometry")
	}

	affected, err := store.GetPublicIncident(ctx, affectedID, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertPublicGeometry(t, affected.PublicGeometry)
	if strings.Contains(string(affected.PublicGeometry), "3.378") || strings.Contains(string(affected.PublicGeometry), "6.5235") {
		t.Fatalf("affected geometry appears to expose unsnapped source coordinates: %s", affected.PublicGeometry)
	}
	collapsed, err := store.GetPublicIncident(ctx, collapsedID, policy)
	if err != nil {
		t.Fatal(err)
	}
	assertPublicGeometry(t, collapsed.PublicGeometry)
	if strings.Contains(string(collapsed.PublicGeometry), "3.3792") || strings.Contains(string(collapsed.PublicGeometry), "6.5244") {
		t.Fatalf("collapsed affected geometry exposed exact center coordinates: %s", collapsed.PublicGeometry)
	}
	if _, err := store.GetPublicIncident(ctx, resolvedID, policy); !errors.Is(err, ErrPublicIncidentNotFound) {
		t.Fatalf("resolved detail error = %v, want ErrPublicIncidentNotFound", err)
	}

}

func assertPublicGeometry(t *testing.T, encoded []byte) {
	t.Helper()
	var geometry struct {
		Type        string            `json:"type"`
		Coordinates []json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(encoded, &geometry); err != nil {
		t.Fatalf("decode public geometry: %v", err)
	}
	if geometry.Type != "Polygon" && geometry.Type != "MultiPolygon" {
		t.Fatalf("public geometry type = %q, want Polygon or MultiPolygon", geometry.Type)
	}
	if len(geometry.Coordinates) == 0 {
		t.Fatal("public geometry coordinates are empty")
	}
}
