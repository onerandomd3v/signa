//go:build integration

package delivery

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
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStoreRecordsAttemptAndIsIdempotent(t *testing.T) {
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
	databaseName := fmt.Sprintf("signa_delivery_test_%d", time.Now().UnixNano())
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
	runDeliveryGoose(t, ctx, testURL.String(), "up")
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	incidentID := "00000000-0000-0000-0000-000000000001"
	alertID := "00000000-0000-0000-0000-000000000002"
	deliveryID := "00000000-0000-0000-0000-000000000003"
	userID := "00000000-0000-0000-0000-000000000004"
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, severity) VALUES ($1, 'OPEN', 'EMERGING', 'HIGH')`, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint) VALUES ($1, $2, 'IMMEDIATE', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'safe', 'test', '[]', now(), 'alert-key', 'fingerprint')`, alertID, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deliveries (id, alert_id, user_id, priority, channel, idempotency_key, payload) VALUES ($1, $2, $3, 'P1', 'TEST', 'delivery-key', '{"safe":"payload"}')`, deliveryID, alertID, userID); err != nil {
		t.Fatal(err)
	}
	store, err := NewPostgresStore(pool, PostgresConfig{MaxAttempts: 2, Backoff: time.Millisecond, Lease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{DeliveryID: deliveryID, AlertID: alertID, IdempotencyKey: "delivery-key"}
	started, err := store.StartAttempt(ctx, request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishAttempt(ctx, started.Attempt, ProviderResult{Response: "accepted"}, "", nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	duplicate, err := store.StartAttempt(ctx, request, time.Now().UTC())
	if err != nil || !duplicate.Terminal {
		t.Fatalf("duplicate start = %+v, %v", duplicate, err)
	}
	var state string
	var attempts, recorded int
	if err := pool.QueryRow(ctx, `SELECT state, attempts FROM deliveries WHERE id = $1`, deliveryID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM delivery_attempts WHERE delivery_id = $1`, deliveryID).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if state != string(StateSucceeded) || attempts != 1 || recorded != 1 {
		t.Fatalf("state/attempts/records = %s/%d/%d", state, attempts, recorded)
	}
}

func runDeliveryGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	goose := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	_, file, _, _ := runtime.Caller(0)
	goose.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if output, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}
