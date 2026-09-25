//go:build integration

package priority

import (
	"context"
	"encoding/json"
	"fmt"
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
	accuracy := 10.0
	userID := uuid.New()
	if _, err := proximity.UpsertUserLocation(ctx, geospatial.RestrictedUserLocation{
		UserID: userID, Point: geospatial.Point{Latitude: 6.5244, Longitude: 3.3792}, AccuracyMeters: &accuracy, ObservedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, proximity, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	results, err := service.EvaluateIncident(ctx, incidentID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].UserID != userID || results[0].Decision.Level != P1 {
		t.Fatalf("priority evaluations = %+v, want one P1 evaluation", results)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "latitude") || strings.Contains(string(encoded), "longitude") {
		t.Fatalf("priority evaluation exposed exact coordinates: %s", encoded)
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
