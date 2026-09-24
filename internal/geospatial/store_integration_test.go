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
		var locationColumnCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name = 'user_location_deletions' AND column_name = 'location'`).Scan(&locationColumnCount); err != nil {
			t.Fatal(err)
		}
		if locationColumnCount != 0 {
			t.Fatal("user location deletion watermark contains a location column")
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
	if replaced, err := store.UpsertUserLocation(ctx, RestrictedUserLocation{UserID: userID, Point: Point{Latitude: 6.5245, Longitude: 3.3792}, AccuracyMeters: &accuracy, ObservedAt: observedAt.Add(time.Minute)}); err != nil || !replaced {
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
	boundaryUser := uuid.MustParse("00000000-0000-0000-0000-000000000004")
	oldUser := uuid.MustParse("00000000-0000-0000-0000-000000000005")
	futureUser := uuid.MustParse("00000000-0000-0000-0000-000000000006")
	for _, user := range []RestrictedUserLocation{
		{UserID: secondUser, Point: Point{Latitude: 6.525, Longitude: 3.3792}, ObservedAt: observedAt},
		{UserID: outsideUser, Point: Point{Latitude: 6.6, Longitude: 3.5}, ObservedAt: observedAt},
		{UserID: boundaryUser, Point: nearTarget, ObservedAt: observedAt.Add(-58 * time.Minute)},
		{UserID: oldUser, Point: nearTarget, ObservedAt: observedAt.Add(-59 * time.Minute)},
		{UserID: futureUser, Point: nearTarget, ObservedAt: observedAt.Add(2 * time.Minute)},
	} {
		if _, err := store.UpsertUserLocation(ctx, user); err != nil {
			t.Fatal(err)
		}
	}
	asOf := observedAt.Add(time.Minute)
	results, err := store.FindUsersWithinRadius(ctx, ProximityQuery{Target: nearTarget, RadiusMeters: 1000, AsOf: asOf, MaxAge: 59 * time.Minute, Limit: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("proximity results = %+v, want fresh, exact-boundary, and current users", results)
	}
	seen := map[uuid.UUID]bool{}
	for _, result := range results {
		seen[result.UserID] = true
	}
	for _, expected := range []uuid.UUID{userID, secondUser, boundaryUser} {
		if !seen[expected] {
			t.Fatalf("proximity results = %+v, missing expected user %s", results, expected)
		}
	}
	for index := 1; index < len(results); index++ {
		if results[index-1].DistanceMeters > results[index].DistanceMeters {
			t.Fatalf("distance ordering = %+v", results)
		}
	}
	var gotAccuracy *float64
	for _, result := range results {
		if result.UserID == userID {
			gotAccuracy = result.AccuracyMeters
		}
	}
	if gotAccuracy == nil || *gotAccuracy != accuracy {
		t.Fatalf("proximity accuracy = %v, want %v", gotAccuracy, accuracy)
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
	limited, err := store.FindUsersWithinRadius(ctx, ProximityQuery{Target: nearTarget, RadiusMeters: 1000, AsOf: asOf, MaxAge: 59 * time.Minute, Limit: 1})
	if err != nil || len(limited) != 1 || limited[0].UserID != boundaryUser {
		t.Fatalf("limited proximity results = (%+v, %v), want closest boundary snapshot", limited, err)
	}
	if _, err := store.FindUsersWithinRadius(ctx, ProximityQuery{Target: nearTarget, RadiusMeters: 0, AsOf: observedAt, MaxAge: time.Hour}); !errors.Is(err, ErrInvalidRadius) {
		t.Fatalf("zero-radius error = %v, want ErrInvalidRadius", err)
	}
	if _, err := store.FindUsersWithinRadius(ctx, ProximityQuery{Target: nearTarget, RadiusMeters: 1000, AsOf: observedAt, MaxAge: time.Hour, Limit: maxProximityLimit + 1}); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("over-limit error = %v, want ErrInvalidLimit", err)
	}
	if err := store.DeleteUserLocation(ctx, userID, observedAt); err != nil {
		t.Fatal(err)
	}
	for _, retryAt := range []time.Time{observedAt.Add(30 * time.Second), observedAt.Add(time.Minute)} {
		if replaced, err := store.UpsertUserLocation(ctx, RestrictedUserLocation{UserID: userID, Point: nearTarget, ObservedAt: retryAt}); err != nil || replaced {
			t.Fatalf("stale post-deletion upsert at %s = (%v, %v), want (false, nil)", retryAt, replaced, err)
		}
	}
	if replaced, err := store.UpsertUserLocation(ctx, RestrictedUserLocation{UserID: userID, Point: nearTarget, ObservedAt: observedAt.Add(2 * time.Hour)}); err != nil || !replaced {
		t.Fatalf("newer post-deletion upsert = (%v, %v), want (true, nil)", replaced, err)
	}
	if err := store.DeleteUserLocation(ctx, userID, observedAt.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_locations WHERE user_id = $1`, userID).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 0 {
		t.Fatalf("deleted snapshot count = %d, want 0", snapshotCount)
	}

	concurrentUser := uuid.MustParse("00000000-0000-0000-0000-000000000007")
	if _, err := store.UpsertUserLocation(ctx, RestrictedUserLocation{UserID: concurrentUser, Point: nearTarget, ObservedAt: observedAt}); err != nil {
		t.Fatal(err)
	}
	upsertDone := make(chan error, 1)
	deleteDone := make(chan error, 1)
	go func() {
		_, err := store.UpsertUserLocation(ctx, RestrictedUserLocation{UserID: concurrentUser, Point: nearTarget, ObservedAt: observedAt})
		upsertDone <- err
	}()
	go func() { deleteDone <- store.DeleteUserLocation(ctx, concurrentUser, observedAt) }()
	if err := <-upsertDone; err != nil {
		t.Fatal(err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_locations WHERE user_id = $1`, concurrentUser).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 0 {
		t.Fatal("concurrent stale upsert resurrected deleted location")
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
