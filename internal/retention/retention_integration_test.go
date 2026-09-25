//go:build integration

package retention

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSweepMinimizesSensitiveDataInBatchesAndIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, databaseName := createRetentionDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), "DROP DATABASE IF EXISTS "+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runRetentionGoose(t, ctx, testURL, "up")

	db, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	old := now.Add(-2 * time.Hour)
	young := now.Add(-30 * time.Minute)
	var reportOne, reportTwo, reportYoung string
	for _, row := range []struct {
		id   *string
		when time.Time
		text string
	}{
		{&reportOne, old, "old one"}, {&reportTwo, old.Add(time.Minute), "old two"}, {&reportYoung, young, "young"},
	} {
		if err := db.QueryRow(ctx, `INSERT INTO reports (raw_text, claimed_location, device_location, location_accuracy, created_at, submitted_at) VALUES ($1, ST_SetSRID(ST_MakePoint(3.3, 6.5), 4326)::geography, ST_SetSRID(ST_MakePoint(3.3, 6.5), 4326)::geography, 10, $2, $2) RETURNING id::text`, row.text, row.when).Scan(row.id); err != nil {
			t.Fatal(err)
		}
	}
	var userOldOne, userOldTwo, userYoung string
	for _, row := range []struct {
		id   *string
		when time.Time
	}{
		{&userOldOne, old}, {&userOldTwo, old.Add(time.Minute)}, {&userYoung, young},
	} {
		*row.id = uuid.NewString()
		if _, err := db.Exec(ctx, `INSERT INTO user_locations (user_id, location, observed_at, updated_at) VALUES ($1, ST_SetSRID(ST_MakePoint(3.3, 6.5), 4326)::geography, $2, $2)`, *row.id, row.when); err != nil {
			t.Fatal(err)
		}
	}
	mediaReport := reportOne
	if _, err := db.Exec(ctx, `INSERT INTO report_media (report_id, object_key, media_type, content_type, size_bytes, created_at) VALUES ($1, 'reports/' || $1 || '/media/old', 'image', 'image/png', 10, $2)`, mediaReport, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at, published_at) VALUES ('old', 'report', $1, '{}'::jsonb, $2, $2), ('young', 'report', $1, '{}'::jsonb, $3, $3)`, reportOne, old, young); err != nil {
		t.Fatal(err)
	}
	var incidentID, alertID, deliveryID string
	if err := db.QueryRow(ctx, `INSERT INTO incidents (status, confidence_state, created_at, updated_at) VALUES ('OPEN', 'EMERGING', $1, $1) RETURNING id::text`, old).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO incident_state_history (incident_id, changed_field, new_value, transitioned_at) VALUES ($1, 'status', 'OPEN', $2)`, incidentID, old); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO alerts (incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, as_of, idempotency_key, request_fingerprint, created_at) VALUES ($1, 'P1', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'safe', 'test', $2, 'retention-alert', 'fingerprint', $2) RETURNING id::text`, incidentID, old).Scan(&alertID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO deliveries (alert_id, user_id, priority, channel, idempotency_key, payload, created_at, updated_at) VALUES ($1, $2, 'P1', 'SSE', 'retention-delivery', '{}'::jsonb, $3, $3) RETURNING id::text`, alertID, userOldOne, old).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO delivery_attempts (delivery_id, attempt_no, operation_key, state, started_at, completed_at) VALUES ($1, 1, 'retention-attempt', 'FAILED', $2, $2); INSERT INTO delivery_quarantine (delivery_id, reason, quarantined_at) VALUES ($1, 'old', $2)`, deliveryID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, token_hash, expires_at, created_at) VALUES (gen_random_uuid(), $1, decode(repeat('aa', 32), 'hex'), $2, $3), (gen_random_uuid(), $1, decode(repeat('bb', 32), 'hex'), $4, $3), (gen_random_uuid(), $1, decode(repeat('cc', 32), 'hex'), $5, $3)`, userOldOne, old.Add(time.Minute), old, old.Add(time.Minute), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE auth_sessions SET revoked_at = $1 WHERE token_hash = decode(repeat('bb', 32), 'hex')`, old.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	policy := Policy{ReportExactLocation: time.Hour, UserLocation: time.Hour, MediaMetadata: time.Hour, OperationalLogs: time.Hour, AuthSessions: time.Hour, SweepInterval: time.Minute, BatchSize: 1}
	counts, err := store.Sweep(ctx, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	if counts.ReportLocations != 2 || counts.UserLocations != 2 || counts.MediaMetadata != 1 || counts.OutboxEvents != 1 || counts.IncidentHistory != 1 || counts.DeliveryAttempts != 1 || counts.DeliveryQuarantine != 1 || counts.AuthSessions != 2 {
		t.Fatalf("sweep counts = %+v", counts)
	}
	assertRetentionCount(t, ctx, db, `SELECT count(*) FROM reports WHERE claimed_location IS NOT NULL OR device_location IS NOT NULL`, 1)
	assertRetentionCount(t, ctx, db, `SELECT count(*) FROM user_locations`, 1)
	assertRetentionCount(t, ctx, db, `SELECT count(*) FROM report_media`, 0)
	assertRetentionCount(t, ctx, db, `SELECT count(*) FROM outbox_events`, 1)
	assertRetentionCount(t, ctx, db, `SELECT count(*) FROM auth_sessions`, 1)
	assertRetentionCount(t, ctx, db, `SELECT count(*) FROM user_location_deletions WHERE user_id = $1`, 1, userOldOne)

	second, err := store.Sweep(ctx, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	if second != (Counts{}) {
		t.Fatalf("second sweep counts = %+v, want zero", second)
	}
	ctxCanceled, cancelSweep := context.WithCancel(context.Background())
	cancelSweep()
	if _, err := store.Sweep(ctxCanceled, now, policy); err == nil {
		t.Fatal("canceled sweep error = nil")
	}
}

func createRetentionDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("signa_retention_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	if _, err := maintenance.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	return testURL.String(), maintenance, databaseName
}

func runRetentionGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cmd := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	_, file, _, _ := runtime.Caller(0)
	cmd.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}

func assertRetentionCount(t *testing.T, ctx context.Context, db *pgxpool.Pool, query string, want int, args ...any) {
	var got int
	if err := db.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}
