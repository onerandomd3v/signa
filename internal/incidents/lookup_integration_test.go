//go:build integration

package incidents

import (
	"context"
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

func TestCandidateLookupIntegration(t *testing.T) {
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

	assertCandidateIndexes(t, ctx, pool)
	store := NewStore(pool)
	asOf := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	latitude, longitude := 6.5244, 3.3792
	insert := func(t *testing.T, id string, eventType string, lat, lon *float64, started, lastSignal, expires, resolved, created *time.Time) {
		t.Helper()
		_, err := pool.Exec(ctx, `
			INSERT INTO incidents (id, event_type, status, confidence_state, center_point, started_at, last_signal_at, expires_at, resolved_at, created_at, updated_at)
			VALUES ($1, $2, 'ACTIVE_TEST', 'UNKNOWN_TEST',
				CASE WHEN $3::double precision IS NULL OR $4::double precision IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography END,
				$5, $6, $7, $8, $9, $9)
		`, id, eventType, lat, lon, started, lastSignal, expires, resolved, created)
		if err != nil {
			t.Fatalf("insert incident %s: %v", id, err)
		}
	}

	recent := asOf.Add(-10 * time.Minute)
	lastMinute := asOf.Add(-time.Minute)
	old := asOf.Add(-2 * time.Hour)
	startedRecent := asOf.Add(-20 * time.Minute)
	createdRecent := asOf.Add(-15 * time.Minute)
	soonExpired := asOf.Add(-time.Minute)
	futureExpiry := asOf.Add(time.Hour)
	resolvedAt := asOf.Add(-time.Minute)
	insert(t, "10000000-0000-0000-0000-000000000001", "possible_gunfire", &latitude, &longitude, nil, &recent, &futureExpiry, nil, &old)
	insert(t, "10000000-0000-0000-0000-000000000002", "possible_gunfire", &latitude, &longitude, nil, &lastMinute, &futureExpiry, nil, &old)
	farLatitude, farLongitude := latitude, longitude+0.05
	insert(t, "10000000-0000-0000-0000-000000000003", "possible_gunfire", &farLatitude, &farLongitude, nil, &recent, &futureExpiry, nil, &recent)
	insert(t, "10000000-0000-0000-0000-000000000004", "road_blockage", &latitude, &longitude, nil, &recent, &futureExpiry, nil, &recent)
	insert(t, "10000000-0000-0000-0000-000000000005", "possible_gunfire", nil, nil, nil, &recent, &futureExpiry, nil, &recent)
	insert(t, "10000000-0000-0000-0000-000000000006", "possible_gunfire", &latitude, &longitude, nil, nil, &futureExpiry, nil, &old)
	insert(t, "10000000-0000-0000-0000-000000000007", "possible_gunfire", &latitude, &longitude, &startedRecent, nil, &futureExpiry, nil, &old)
	insert(t, "10000000-0000-0000-0000-000000000008", "possible_gunfire", &latitude, &longitude, nil, nil, &futureExpiry, nil, &createdRecent)
	insert(t, "10000000-0000-0000-0000-000000000009", "possible_gunfire", &latitude, &longitude, nil, &recent, &futureExpiry, &resolvedAt, &recent)
	insert(t, "10000000-0000-0000-0000-000000000010", "possible_gunfire", &latitude, &longitude, nil, &recent, &soonExpired, nil, &recent)
	insert(t, "10000000-0000-0000-0000-000000000011", "possible_gunfire", &latitude, &longitude, nil, &recent, &futureExpiry, nil, &recent)

	t.Run("candidate filters", func(t *testing.T) {
		candidates, err := store.LookupCandidates(ctx, CandidateLookup{EventType: " possible_gunfire ", Latitude: latitude, Longitude: longitude, RadiusMeters: 1000, AsOf: asOf, TimeWindow: time.Hour, Limit: 100})
		if err != nil {
			t.Fatalf("LookupCandidates() error = %v", err)
		}
		got := map[uuid.UUID]Candidate{}
		for _, candidate := range candidates {
			got[candidate.ID] = candidate
		}
		for _, id := range []string{
			"10000000-0000-0000-0000-000000000001",
			"10000000-0000-0000-0000-000000000002",
			"10000000-0000-0000-0000-000000000007",
			"10000000-0000-0000-0000-000000000008",
			"10000000-0000-0000-0000-000000000011",
		} {
			if _, ok := got[uuid.MustParse(id)]; !ok {
				t.Errorf("expected candidate %s", id)
			}
		}
		for _, id := range []string{
			"10000000-0000-0000-0000-000000000003",
			"10000000-0000-0000-0000-000000000004",
			"10000000-0000-0000-0000-000000000005",
			"10000000-0000-0000-0000-000000000006",
			"10000000-0000-0000-0000-000000000009",
			"10000000-0000-0000-0000-000000000010",
		} {
			if _, ok := got[uuid.MustParse(id)]; ok {
				t.Errorf("unexpected candidate %s", id)
			}
		}
		for _, candidate := range candidates {
			if candidate.DistanceMeters < 0 || candidate.DistanceMeters > 1000 {
				t.Errorf("candidate %s distance = %f, outside radius", candidate.ID, candidate.DistanceMeters)
			}
		}
	})

	t.Run("distance freshness and stable id ordering", func(t *testing.T) {
		orderEvent := "ordering_test"
		nearLon := longitude
		farOrderLon := longitude + 0.003
		orderOld := asOf.Add(-10 * time.Minute)
		orderNew := asOf.Add(-time.Minute)
		insert(t, "20000000-0000-0000-0000-000000000012", orderEvent, &latitude, &nearLon, nil, &orderOld, &futureExpiry, nil, &recent)
		insert(t, "20000000-0000-0000-0000-000000000010", orderEvent, &latitude, &nearLon, nil, &orderNew, &futureExpiry, nil, &recent)
		insert(t, "20000000-0000-0000-0000-000000000011", orderEvent, &latitude, &nearLon, nil, &orderNew, &futureExpiry, nil, &recent)
		insert(t, "20000000-0000-0000-0000-000000000013", orderEvent, &latitude, &farOrderLon, nil, &asOf, &futureExpiry, nil, &recent)
		candidates, err := store.LookupCandidates(ctx, CandidateLookup{EventType: orderEvent, Latitude: latitude, Longitude: longitude, RadiusMeters: 1000, AsOf: asOf, TimeWindow: time.Hour, Limit: 100})
		if err != nil {
			t.Fatalf("LookupCandidates() error = %v", err)
		}
		want := []uuid.UUID{uuid.MustParse("20000000-0000-0000-0000-000000000010"), uuid.MustParse("20000000-0000-0000-0000-000000000011"), uuid.MustParse("20000000-0000-0000-0000-000000000012"), uuid.MustParse("20000000-0000-0000-0000-000000000013")}
		if len(candidates) != len(want) {
			t.Fatalf("candidate count = %d, want %d", len(candidates), len(want))
		}
		for i, candidate := range candidates {
			if candidate.ID != want[i] {
				t.Errorf("candidate[%d] = %s, want %s", i, candidate.ID, want[i])
			}
		}
		limited, err := store.LookupCandidates(ctx, CandidateLookup{EventType: orderEvent, Latitude: latitude, Longitude: longitude, RadiusMeters: 1000, AsOf: asOf, TimeWindow: time.Hour, Limit: 2})
		if err != nil {
			t.Fatalf("limited LookupCandidates() error = %v", err)
		}
		if len(limited) != 2 || limited[0].ID != want[0] || limited[1].ID != want[1] {
			t.Fatalf("limited candidates = %#v, want first two ordered candidates", limited)
		}
	})

	t.Run("lookup is read-only", func(t *testing.T) {
		var beforeIncidents, beforeReports, beforeHistory int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM incidents`).Scan(&beforeIncidents); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM reports`).Scan(&beforeReports); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM incident_state_history`).Scan(&beforeHistory); err != nil {
			t.Fatal(err)
		}
		if _, err := store.LookupCandidates(ctx, CandidateLookup{EventType: "possible_gunfire", Latitude: latitude, Longitude: longitude, RadiusMeters: 1000, AsOf: asOf, TimeWindow: time.Hour, Limit: 10}); err != nil {
			t.Fatal(err)
		}
		var afterIncidents, afterReports, afterHistory int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM incidents`).Scan(&afterIncidents); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM reports`).Scan(&afterReports); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM incident_state_history`).Scan(&afterHistory); err != nil {
			t.Fatal(err)
		}
		if beforeIncidents != afterIncidents || beforeReports != afterReports || beforeHistory != afterHistory {
			t.Fatalf("lookup changed counts: incidents %d/%d, reports %d/%d, history %d/%d", beforeIncidents, afterIncidents, beforeReports, afterReports, beforeHistory, afterHistory)
		}
	})

	runGoose(t, ctx, testDatabaseURL, "down")
	runGoose(t, ctx, testDatabaseURL, "down")
	assertIndexMissing(t, ctx, pool, "incidents_center_point_gist_idx")
	assertTableExists(t, ctx, pool, "incidents")
	runGoose(t, ctx, testDatabaseURL, "up")
	runGoose(t, ctx, testDatabaseURL, "up")
	assertIndexExists(t, ctx, pool, "incidents_center_point_gist_idx")
}

func createIntegrationDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	databaseName := fmt.Sprintf("signa_incident_lookup_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatalf("connect to maintenance database: %v", err)
	}
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+databaseName); err != nil {
		_ = maintenance.Close(context.Background())
		t.Fatalf("create test database: %v", err)
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
	output, err := goose.CombinedOutput()
	if err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}

func assertCandidateIndexes(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT indexname, indexdef FROM pg_indexes WHERE tablename = 'incidents'`)
	if err != nil {
		t.Fatalf("query incident indexes: %v", err)
	}
	defer rows.Close()
	indexes := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatalf("scan incident index: %v", err)
		}
		indexes[name] = strings.ToLower(definition)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate incident indexes: %v", err)
	}
	if definition := indexes["incidents_center_point_gist_idx"]; !strings.Contains(definition, "gist") || !strings.Contains(definition, "center_point") {
		t.Fatalf("center point index definition = %q, want GiST center_point index", definition)
	}
	if definition := indexes["incidents_candidate_lookup_idx"]; !strings.Contains(definition, "event_type") || !strings.Contains(definition, "coalesce") || !strings.Contains(definition, "resolved_at is null") {
		t.Fatalf("candidate index definition = %q, want event/time unresolved lookup index", definition)
	}
}

func assertIndexExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("check index %s: %v", name, err)
	}
	if !exists {
		t.Fatalf("index %s does not exist", name)
	}
}

func assertIndexMissing(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("check index %s: %v", name, err)
	}
	if exists {
		t.Fatalf("index %s still exists", name)
	}
}

func assertTableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	if !exists {
		t.Fatalf("table %s does not exist", table)
	}
}
