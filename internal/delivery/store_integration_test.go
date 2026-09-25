//go:build integration

package delivery

import (
	"context"
	"errors"
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

func TestPostgresStoreFencesStaleAttemptAndProtectsFinalLease(t *testing.T) {
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
	databaseName := fmt.Sprintf("signa_delivery_fence_test_%d", time.Now().UnixNano())
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
	incidentID := "00000000-0000-0000-0000-000000000011"
	alertID := "00000000-0000-0000-0000-000000000012"
	userID := "00000000-0000-0000-0000-000000000013"
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, severity) VALUES ($1, 'OPEN', 'EMERGING', 'HIGH')`, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint) VALUES ($1, $2, 'IMMEDIATE', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'safe', 'test', '[]', now(), $3, $3)`, alertID, incidentID, "alert-fence-key"); err != nil {
		t.Fatal(err)
	}
	staleDeliveryID := "00000000-0000-0000-0000-000000000014"
	finalDeliveryID := "00000000-0000-0000-0000-000000000015"
	for _, item := range []struct{ id, key string }{{staleDeliveryID, "stale-delivery-key"}, {finalDeliveryID, "final-delivery-key"}} {
		if _, err := pool.Exec(ctx, `INSERT INTO deliveries (id, alert_id, user_id, priority, channel, idempotency_key, payload) VALUES ($1, $2, $3, 'P1', 'TEST', $4, '{"safe":"payload"}')`, item.id, alertID, userID, item.key); err != nil {
			t.Fatal(err)
		}
	}

	staleStore, err := NewPostgresStore(pool, PostgresConfig{MaxAttempts: 3, Backoff: time.Millisecond, Lease: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{DeliveryID: staleDeliveryID, AlertID: alertID, IdempotencyKey: "stale-delivery-key"}
	attemptOne, err := staleStore.StartAttempt(ctx, request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	attemptTwo, err := staleStore.StartAttempt(ctx, request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if attemptTwo.Attempt.Attempt != 2 {
		t.Fatalf("attempt two = %+v", attemptTwo.Attempt)
	}
	late, err := staleStore.FinishAttempt(ctx, attemptOne.Attempt, ProviderResult{}, FailureTransient, errors.New("late timeout"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if !late.Stale || late.Terminal {
		t.Fatalf("late result = %+v, want stale nonterminal", late)
	}
	var state State
	var active *int
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT state, active_attempt_no, attempts FROM deliveries WHERE id = $1`, staleDeliveryID).Scan(&state, &active, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != StateInFlight || active == nil || *active != 2 || attempts != 2 {
		t.Fatalf("stale completion mutated state: state=%s active=%v attempts=%d", state, active, attempts)
	}
	if _, err := staleStore.FinishAttempt(ctx, attemptTwo.Attempt, ProviderResult{Response: "accepted"}, "", nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM deliveries WHERE id = $1`, staleDeliveryID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != StateSucceeded {
		t.Fatalf("attempt two state = %s, want succeeded", state)
	}

	finalStore, err := NewPostgresStore(pool, PostgresConfig{MaxAttempts: 1, Backoff: time.Millisecond, Lease: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	finalRequest := Request{DeliveryID: finalDeliveryID, AlertID: alertID, IdempotencyKey: "final-delivery-key"}
	if _, err := finalStore.StartAttempt(ctx, finalRequest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stillActive, err := finalStore.StartAttempt(ctx, finalRequest, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if stillActive.Terminal || !stillActive.NotDue {
		t.Fatalf("active final attempt = %+v, want nonterminal not-due", stillActive)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM deliveries WHERE id = $1`, finalDeliveryID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != StateInFlight {
		t.Fatalf("active final state = %s, want in-flight", state)
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
