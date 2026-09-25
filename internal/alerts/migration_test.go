//go:build integration

package alerts

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

	"github.com/jackc/pgx/v5"
)

func TestAlertMigrationUpDownReapplyAndConstraints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("signa_alert_migration_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = maintenance.Exec(context.Background(), "DROP DATABASE IF EXISTS "+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	if _, err := maintenance.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	runGooseAlerts(t, ctx, testURL.String(), "up")
	connection, err := pgx.Connect(ctx, testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())

	var exists bool
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.alerts') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("alerts table exists=%v err=%v", exists, err)
	}
	var indexCount int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'alerts' AND indexname IN ('alerts_incident_created_idx', 'alerts_supersedes_alert_id_idx')`).Scan(&indexCount); err != nil || indexCount != 2 {
		t.Fatalf("alert index count = %d, err=%v", indexCount, err)
	}
	if _, err := connection.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state) VALUES ('00000000-0000-0000-0000-000000000001', 'OPEN', 'EMERGING')`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `INSERT INTO alerts (incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint) VALUES ('00000000-0000-0000-0000-000000000001', 'IMMEDIATE', 'EMERGING', 'CRITICAL', 'OPEN', 'P1', 'FRESH', 'safe message', 'signa.alert-eligibility.v1', '[]', now(), 'migration-key', 'fingerprint-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, `INSERT INTO alerts (incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint) VALUES ('00000000-0000-0000-0000-000000000001', 'IMMEDIATE', 'EMERGING', 'CRITICAL', 'OPEN', 'P1', 'FRESH', 'safe message', 'signa.alert-eligibility.v1', '[]', now(), 'migration-key', 'fingerprint-1')`); err == nil {
		t.Fatal("duplicate idempotency key was accepted")
	}
	if _, err := connection.Exec(ctx, `INSERT INTO alerts (incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint) VALUES ('00000000-0000-0000-0000-000000000099', 'IMMEDIATE', 'EMERGING', 'CRITICAL', 'OPEN', 'P1', 'FRESH', 'safe message', 'signa.alert-eligibility.v1', '[]', now(), 'foreign-key-key', 'fingerprint-foreign')`); err == nil {
		t.Fatal("foreign incident reference was accepted")
	}
	runGooseAlerts(t, ctx, testURL.String(), "down")
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.alerts') IS NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("alerts table remains after down: exists=%v err=%v", exists, err)
	}
	runGooseAlerts(t, ctx, testURL.String(), "up")
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.alerts') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		t.Fatalf("alerts table missing after reapply: exists=%v err=%v", exists, err)
	}
}

func runGooseAlerts(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
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
