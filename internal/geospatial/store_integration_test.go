//go:build integration

package geospatial

import (
	"context"
	"encoding/json"
	"errors"
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
)

const integrationDatabaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"

func TestUserLocationStoreIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = integrationDatabaseURL
	}
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

	t.Run("schema and indexes", func(t *testing.T) {
		var locationType string
		if err := pool.QueryRow(ctx, `SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute AS a JOIN pg_class AS c ON c.oid = a.attrelid WHERE c.relname = 'user_locations' AND a.attname = 'location' AND a.attnum > 0`).Scan(&locationType); err != nil {
			t.Fatal(err)
		}
		if locationType != "geography(Point,4326)" {
			t.Fatalf("location type = %q, want geography(Point,4326)", locationType)
		}
		for _, indexName := range []string{"user_locations_location_gist_idx", "incidents_affected_geometry_gist_idx"} {
			var definition string
			if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname = $1`, indexName).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.ToLower(definition), "gist") {
				t.Fatalf("index %s = %q, want GiST", indexName, definition)
			}
		}
	})

	observedAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	accuracy := 20.0
	userID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	location := RestrictedUserLocation{UserID: userID, Point: Point{Latitude: 6.5244, Longitude: 3.3792}, AccuracyMeters: &accuracy, ObservedAt: observedAt}
	if replaced, err := store.UpsertUserLocation(ctx, location); err != nil || !replaced {
		t.Fatalf("initial upsert = (%v, %v), want (true, nil)", replaced, err)
	}
	if replaced, err := store.UpsertUserLocation(ctx, RestrictedUserLocation{UserID: userID, Point: Point{Latitude: 6.6, Longitude: 3.5}, ObservedAt: observedAt.Add(-time.Minute)}); err != nil || replaced {
		t.Fatalf("stale upsert = (%v, %v), want (false, nil)", replaced, err)
	}
	var latitude, longitude float64
	if err := pool.QueryRow(ctx, `SELECT ST_Y(location::geometry), ST_X(location::geometry) FROM user_locations WHERE user_id = $1`, userID).Scan(&latitude, &longitude); err != nil {
		t.Fatal(err)
	}
	if latitude != location.Point.Latitude || longitude != location.Point.Longitude {
		t.Fatalf("stale upsert changed location = (%v, %v)", latitude, longitude)
	}
	if replaced, err := store.UpsertUserLocation(ctx, RestrictedUserLocation{UserID: userID, Point: Point{Latitude: 6.5245, Longitude: 3.3792}, ObservedAt: observedAt.Add(time.Minute)}); err != nil || !replaced {
		t.Fatalf("newer upsert = (%v, %v), want (true, nil)", replaced, err)
	}
	var snapshotCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_locations WHERE user_id = $1`, userID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 1 {
		t.Fatalf("snapshot count = %d, want 1", snapshotCount)
	}

	nearTarget := Point{Latitude: 6.5244, Longitude: 3.3792}
	secondUser := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	outsideUser := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	for _, user := range []RestrictedUserLocation{
		{UserID: secondUser, Point: Point{Latitude: 6.525, Longitude: 3.3792}, ObservedAt: observedAt},
		{UserID: outsideUser, Point: Point{Latitude: 6.6, Longitude: 3.5}, ObservedAt: observedAt},
	} {
		if _, err := store.UpsertUserLocation(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	results, err := store.FindUsersWithinRadius(ctx, nearTarget, 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].UserID != userID || results[1].UserID != secondUser {
		t.Fatalf("proximity results = %+v, want deterministic in-radius users", results)
	}
	if results[0].DistanceMeters > results[1].DistanceMeters {
		t.Fatalf("distance ordering = %v then %v", results[0].DistanceMeters, results[1].DistanceMeters)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	for _, restrictedField := range []string{"latitude", "longitude", "location"} {
		if strings.Contains(string(encoded), restrictedField) {
			t.Fatalf("proximity result exposes restricted field %q: %s", restrictedField, encoded)
		}
	}
	limited, err := store.FindUsersWithinRadius(ctx, nearTarget, 1000, 1)
	if err != nil || len(limited) != 1 || limited[0].UserID != userID {
		t.Fatalf("limited proximity results = (%+v, %v)", limited, err)
	}
	if _, err := store.FindUsersWithinRadius(ctx, nearTarget, 0, 0); !errors.Is(err, ErrInvalidRadius) {
		t.Fatalf("zero-radius error = %v, want ErrInvalidRadius", err)
	}
	if _, err := store.FindUsersWithinRadius(ctx, nearTarget, 1000, maxProximityLimit+1); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("over-limit error = %v, want ErrInvalidLimit", err)
	}
	if err := store.DeleteUserLocation(ctx, userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_locations WHERE user_id = $1`, userID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 0 {
		t.Fatalf("deleted snapshot count = %d, want 0", snapshotCount)
	}
}

func createIntegrationDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("signa_geospatial_test_%d", time.Now().UnixNano())
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

func runGoose(t *testing.T, ctx context.Context, databaseURL string, command string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	goose := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	goose.Dir = repoRoot
	if output, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}
