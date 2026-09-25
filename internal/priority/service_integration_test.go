//go:build integration

package priority

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/geospatial"
	"github.com/onerandomd3v/signa/internal/routing"
)

func TestServiceUsesPrivacySafeProximityStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, databaseName := createPriorityIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runPriorityGoose(t, ctx, testURL)

	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	proximity, err := geospatial.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	incidentID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO incidents (id, status, confidence_state, severity, center_point, started_at, last_signal_at, created_at, updated_at)
		VALUES ($1, 'OPEN', 'EMERGING', 'CRITICAL', ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography, $2, $2, $2, $2)
	`, incidentID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	incidentLatitude := 6.5244
	incidentLongitude := 3.3792
	metersPerLongitude := 111320 * math.Cos(incidentLatitude*math.Pi/180)
	pointAtDistance := func(distanceMeters float64) geospatial.Point {
		return geospatial.Point{Latitude: incidentLatitude, Longitude: incidentLongitude + distanceMeters/metersPerLongitude}
	}
	possibleUser := uuid.New()
	definitelyOutsideUser := uuid.New()
	definitelyInsideUser := uuid.New()
	possibleAccuracy := 100.0
	zeroAccuracy := 0.0
	insideAccuracy := 10.0
	for _, location := range []geospatial.RestrictedUserLocation{
		{UserID: possibleUser, Point: pointAtDistance(550), AccuracyMeters: &possibleAccuracy, ObservedAt: now.Add(-time.Minute)},
		{UserID: definitelyOutsideUser, Point: pointAtDistance(550), AccuracyMeters: &zeroAccuracy, ObservedAt: now.Add(-time.Minute)},
		{UserID: definitelyInsideUser, Point: pointAtDistance(480), AccuracyMeters: &insideAccuracy, ObservedAt: now.Add(-time.Minute)},
	} {
		if _, err := proximity.UpsertUserLocation(ctx, location); err != nil {
			t.Fatal(err)
		}
	}
	service, err := NewService(pool, proximity, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	results, err := service.EvaluateIncident(ctx, incidentID, now)
	if err != nil {
		t.Fatal(err)
	}
	decisions := make(map[uuid.UUID]Decision)
	for _, result := range results {
		decisions[result.UserID] = result.Decision
	}
	if len(results) != 2 {
		t.Fatalf("priority evaluations = %+v, want possible and definite users only", results)
	}
	if decisions[possibleUser].Level != P3 || !contains(decisions[possibleUser].Reasons, "location_uncertain") || !contains(decisions[possibleUser].Reasons, "possibly_within_p2_radius") {
		t.Fatalf("possible user decision = %+v, want uncertain P3", decisions[possibleUser])
	}
	if _, ok := decisions[definitelyOutsideUser]; ok {
		t.Fatalf("definitely outside user was returned: %+v", decisions[definitelyOutsideUser])
	}
	if decisions[definitelyInsideUser].Level != P2 || !contains(decisions[definitelyInsideUser].Reasons, "within_p2_radius") {
		t.Fatalf("definitely inside user decision = %+v, want P2", decisions[definitelyInsideUser])
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "latitude") || strings.Contains(string(encoded), "longitude") {
		t.Fatalf("priority evaluation exposed exact coordinates: %s", encoded)
	}
}

func TestServiceEvaluatesRouteRelevanceWithoutExposingRouteGeometry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, databaseName := createPriorityIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runPriorityGoose(t, ctx, testURL)

	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	proximityStore, err := geospatial.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	incidentID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO incidents (id, status, confidence_state, severity, center_point, affected_geometry, started_at, last_signal_at, created_at, updated_at)
		VALUES ($1, 'OPEN', 'EMERGING', 'CRITICAL',
		        ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography,
		        ST_GeomFromText('POLYGON((3.37 6.51, 3.39 6.51, 3.39 6.54, 3.37 6.54, 3.37 6.51))', 4326)::geography,
		        $2, $2, $2, $2)
	`, incidentID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	accuracy := 0.0
	proximity := geospatial.ProximityResult{UserID: userID, DistanceMeters: 1000, AccuracyMeters: &accuracy, ObservedAt: now.Add(-time.Minute)}
	route := routing.Route{Geometry: routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.36, 6.525}, {3.40, 6.525}}}}
	service, err := NewService(pool, proximityStore, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := service.EvaluateIncidentForRoute(ctx, incidentID, proximity, route, now)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.UserID != userID || evaluation.Decision.Level != P2 || !contains(evaluation.Decision.Reasons, "route_relevant") || !contains(evaluation.Decision.Reasons, "route_promoted_to_p2") {
		t.Fatalf("route evaluation = %+v, want route-promoted P2", evaluation)
	}
	encoded, err := json.Marshal(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "3.36") || strings.Contains(string(encoded), "6.525") || strings.Contains(string(encoded), "coordinates") {
		t.Fatalf("route evaluation exposed route geometry: %s", encoded)
	}

	nonIntersecting := routing.Route{Geometry: routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.36, 6.60}, {3.40, 6.60}}}}
	nonRelevant, err := service.EvaluateIncidentForRoute(ctx, incidentID, proximity, nonIntersecting, now)
	if err != nil {
		t.Fatal(err)
	}
	if nonRelevant.Decision.Level != None || !contains(nonRelevant.Decision.Reasons, "route_not_relevant") {
		t.Fatalf("non-relevant route evaluation = %+v, want v1 NONE", nonRelevant)
	}
}

func createPriorityIntegrationDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("signa_priority_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+databaseName); err != nil {
		_ = maintenance.Close(context.Background())
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	return testURL.String(), maintenance, databaseName
}

func runPriorityGoose(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	command := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, "up")
	command.Dir = repoRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("goose up: %v\n%s", err, output)
	}
}
