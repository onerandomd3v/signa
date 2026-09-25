//go:build integration

package alerts

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
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/priority"
)

func TestStoreCreateIdempotencyEligibilityAndSupersession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	testURL, maintenance, databaseName := createAlertTestDatabase(t, ctx)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), "DROP DATABASE IF EXISTS "+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runAlertGoose(t, ctx, testURL, "up")
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	incidentOne := "00000000-0000-0000-0000-000000000001"
	incidentTwo := "00000000-0000-0000-0000-000000000002"
	for _, id := range []string{incidentOne, incidentTwo} {
		if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, severity) VALUES ($1, 'OPEN', 'EMERGING', 'CRITICAL')`, id); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(pool)
	request := CreateRequest{IncidentID: incidentOne, Eligibility: EligibilityInput{Status: priority.StatusOpen, Freshness: FreshnessFresh, Confidence: priority.ConfidenceEmerging, Severity: priority.SeverityCritical, Priority: priority.P1}, Message: "safe alert message", AsOf: time.Date(2026, 9, 25, 1, 0, 0, 0, time.UTC), IdempotencyKey: "alert-test-1"}
	created, err := store.Create(ctx, request)
	if err != nil || created.Reused || !created.Decision.Eligible || created.Alert.ID == "" {
		t.Fatalf("created = %+v, err = %v", created, err)
	}
	var alertCount, outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE idempotency_key = $1`, request.IdempotencyKey).Scan(&alertCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type = 'alert.created' AND aggregate_id = $1`, created.Alert.ID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if alertCount != 1 || outboxCount != 1 {
		t.Fatalf("alert/outbox counts = %d/%d", alertCount, outboxCount)
	}

	duplicate, err := store.Create(ctx, request)
	if err != nil || !duplicate.Reused || duplicate.Alert.ID != created.Alert.ID {
		t.Fatalf("duplicate = %+v, err = %v", duplicate, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE idempotency_key = $1`, request.IdempotencyKey).Scan(&alertCount); err != nil {
		t.Fatal(err)
	}
	if alertCount != 1 {
		t.Fatalf("duplicate alert count = %d", alertCount)
	}
	conflicting := request
	conflicting.Message = "different safe alert message"
	if _, err := store.Create(ctx, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting idempotency error = %v, want ErrIdempotencyConflict", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type = 'alert.created' AND aggregate_id = $1`, created.Alert.ID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("conflicting idempotency outbox count = %d", outboxCount)
	}

	payloadBytes := []byte{}
	if err := pool.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE event_type = 'alert.created' AND aggregate_id = $1`, created.Alert.ID).Scan(&payloadBytes); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"alert_id", "incident_id"} {
		if _, ok := payload[field]; !ok {
			t.Errorf("event payload missing %q", field)
		}
	}
	for _, privateField := range []string{"alert_type", "confidence_snapshot", "severity_snapshot", "priority_snapshot", "created_at", "message", "report_text", "reporter_id", "device_location", "route_geometry"} {
		if _, ok := payload[privateField]; ok {
			t.Errorf("event payload contains private field %q", privateField)
		}
	}

	ineligible := request
	ineligible.IdempotencyKey = "alert-ineligible"
	ineligible.Eligibility.Priority = priority.P3
	result, err := store.Create(ctx, ineligible)
	if !errors.Is(err, ErrIneligible) || result.Decision.Eligible {
		t.Fatalf("ineligible result = %+v, err = %v", result, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE idempotency_key = $1`, ineligible.IdempotencyKey).Scan(&alertCount); err != nil {
		t.Fatal(err)
	}
	if alertCount != 0 {
		t.Fatalf("ineligible alert count = %d", alertCount)
	}

	superseding := request
	superseding.IdempotencyKey = "alert-test-2"
	superseding.SupersedesAlertID = &created.Alert.ID
	second, err := store.Create(ctx, superseding)
	if err != nil || second.Alert.SupersedesAlertID == nil || *second.Alert.SupersedesAlertID != created.Alert.ID {
		t.Fatalf("superseding = %+v, err = %v", second, err)
	}

	wrongIncident := superseding
	wrongIncident.IncidentID = incidentTwo
	wrongIncident.IdempotencyKey = "alert-test-wrong-incident"
	if _, err := store.Create(ctx, wrongIncident); !errors.Is(err, ErrSupersedesOtherIncident) {
		t.Fatalf("wrong incident error = %v", err)
	}

	foreign := request
	foreign.IncidentID = "00000000-0000-0000-0000-000000000099"
	foreign.IdempotencyKey = "alert-foreign-incident"
	if _, err := store.Create(ctx, foreign); err == nil {
		t.Fatal("foreign incident alert unexpectedly created")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE idempotency_key = $1`, foreign.IdempotencyKey).Scan(&alertCount); err != nil {
		t.Fatal(err)
	}
	if alertCount != 0 {
		t.Fatalf("foreign incident alert count = %d", alertCount)
	}
}

func createAlertTestDatabase(t *testing.T, ctx context.Context) (string, *pgx.Conn, string) {
	t.Helper()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("signa_alert_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
		_ = maintenance.Close(context.Background())
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	return testURL.String(), maintenance, databaseName
}

func runAlertGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	goose := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	_, file, _, _ := runtime.Caller(0)
	goose.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	output, err := goose.CombinedOutput()
	if err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}
