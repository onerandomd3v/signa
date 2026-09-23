//go:build integration

package reports

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
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const integrationDatabaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"

func TestStoreIntegration(t *testing.T) {
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = integrationDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	testDatabaseURL, maintenanceConnection, databaseName := createIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenanceConnection.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenanceConnection.Close(context.Background())
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

	store := NewStore(pool)

	t.Run("text-only report commits report and outbox", func(t *testing.T) {
		acknowledgement, err := store.Ingest(ctx, "text-only-key", IngestRequest{RawText: "Road blocked near the market"})
		if err != nil {
			t.Fatalf("Ingest() error = %v", err)
		}
		assertReportExists(t, ctx, pool, acknowledgement.ReportID)
		assertOutboxEvent(t, ctx, pool, acknowledgement.ReportID)

		var reporterID, normalizedText, sourceType, eyewitnessClaim, language, evidenceState *string
		var claimedLocation, observedAt *string
		err = pool.QueryRow(ctx, `
			SELECT reporter_id::text, normalized_text, source_type, eyewitness_claim,
			       language, evidence_state, claimed_location::text, observed_at::text
			FROM reports WHERE id = $1
		`, acknowledgement.ReportID).Scan(&reporterID, &normalizedText, &sourceType, &eyewitnessClaim, &language, &evidenceState, &claimedLocation, &observedAt)
		if err != nil {
			t.Fatalf("load unknown report fields: %v", err)
		}
		for name, value := range map[string]*string{
			"reporter_id": reporterID, "normalized_text": normalizedText, "source_type": sourceType,
			"eyewitness_claim": eyewitnessClaim, "language": language, "evidence_state": evidenceState,
			"claimed_location": claimedLocation, "observed_at": observedAt,
		} {
			if value != nil {
				t.Errorf("%s = %q, want NULL for initial ingestion", *value, name)
			}
		}
	})

	t.Run("device location remains private and persists", func(t *testing.T) {
		accuracy := 12.5
		acknowledgement, err := store.Ingest(ctx, "location-key", IngestRequest{
			RawText: "Smoke near the junction",
			DeviceLocation: &DeviceLocation{
				Latitude:  6.5244,
				Longitude: 3.3792,
				Accuracy:  &accuracy,
			},
		})
		if err != nil {
			t.Fatalf("Ingest() error = %v", err)
		}
		var latitude, longitude, storedAccuracy float64
		if err := pool.QueryRow(ctx, `
			SELECT ST_Y(device_location::geometry), ST_X(device_location::geometry), location_accuracy
			FROM reports WHERE id = $1
		`, acknowledgement.ReportID).Scan(&latitude, &longitude, &storedAccuracy); err != nil {
			t.Fatalf("load device location: %v", err)
		}
		if latitude != 6.5244 || longitude != 3.3792 || storedAccuracy != accuracy {
			t.Fatalf("location = (%v, %v, %v), want (%v, %v, %v)", latitude, longitude, storedAccuracy, 6.5244, 3.3792, accuracy)
		}
	})

	t.Run("same request is idempotent", func(t *testing.T) {
		input := IngestRequest{RawText: "One report only"}
		first, err := store.Ingest(ctx, "duplicate-key", input)
		if err != nil {
			t.Fatalf("first Ingest() error = %v", err)
		}
		second, err := store.Ingest(ctx, "duplicate-key", input)
		if err != nil {
			t.Fatalf("second Ingest() error = %v", err)
		}
		if first != second {
			t.Fatalf("acknowledgements differ: first=%+v second=%+v", first, second)
		}
		assertCount(t, ctx, pool, `SELECT count(*) FROM reports WHERE idempotency_key = $1`, "duplicate-key", 1)
		assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, first.ReportID, 1)
	})

	t.Run("concurrent duplicate requests are idempotent", func(t *testing.T) {
		const requestCount = 6
		acknowledgements := make([]Acknowledgement, requestCount)
		errorsByRequest := make([]error, requestCount)
		var waitGroup sync.WaitGroup
		for index := range acknowledgements {
			waitGroup.Add(1)
			go func(index int) {
				defer waitGroup.Done()
				acknowledgements[index], errorsByRequest[index] = store.Ingest(ctx, "concurrent-key", IngestRequest{RawText: "Concurrent report"})
			}(index)
		}
		waitGroup.Wait()

		for index, err := range errorsByRequest {
			if err != nil {
				t.Fatalf("request %d error = %v", index, err)
			}
			if acknowledgements[index] != acknowledgements[0] {
				t.Fatalf("request %d acknowledgement = %+v, want %+v", index, acknowledgements[index], acknowledgements[0])
			}
		}
		assertCount(t, ctx, pool, `SELECT count(*) FROM reports WHERE idempotency_key = $1`, "concurrent-key", 1)
		assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, acknowledgements[0].ReportID, 1)
	})

	t.Run("different request with same key conflicts", func(t *testing.T) {
		if _, err := store.Ingest(ctx, "conflict-key", IngestRequest{RawText: "Original"}); err != nil {
			t.Fatalf("first Ingest() error = %v", err)
		}
		_, err := store.Ingest(ctx, "conflict-key", IngestRequest{RawText: "Changed"})
		if !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("second Ingest() error = %v, want ErrIdempotencyConflict", err)
		}
		assertCount(t, ctx, pool, `SELECT count(*) FROM reports WHERE idempotency_key = $1`, "conflict-key", 1)
	})

	t.Run("failed transaction leaves no report", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin transaction: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()

		var reportID string
		if err := tx.QueryRow(ctx, `INSERT INTO reports (raw_text) VALUES ('atomic test') RETURNING id::text`).Scan(&reportID); err != nil {
			t.Fatalf("insert report: %v", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload) VALUES ('report.created', 'report', $1, 'not-json'::jsonb)`, reportID); err == nil {
			t.Fatal("invalid outbox insert error = nil")
		}
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rollback transaction: %v", err)
		}
		assertCount(t, ctx, pool, `SELECT count(*) FROM reports WHERE id = $1`, reportID, 0)
	})
}

func createIntegrationDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	databaseName := fmt.Sprintf("signa_report_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenanceConnection, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatalf("connect to maintenance database: %v", err)
	}
	if _, err := maintenanceConnection.Exec(ctx, `CREATE DATABASE `+databaseName); err != nil {
		_ = maintenanceConnection.Close(context.Background())
		t.Fatalf("create test database: %v", err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	return testURL.String(), maintenanceConnection, databaseName
}

func runGoose(t *testing.T, ctx context.Context, databaseURL string, command string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	goose := exec.CommandContext(ctx, "go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	goose.Dir = repoRoot
	output, err := goose.CombinedOutput()
	if err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}

func assertReportExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reportID string) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reports WHERE id = $1`, reportID).Scan(&count); err != nil {
		t.Fatalf("query report: %v", err)
	}
	if count != 1 {
		t.Fatalf("report count = %d, want 1", count)
	}
}

func assertOutboxEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reportID string) {
	t.Helper()
	var eventType, aggregateType string
	var payload []byte
	if err := pool.QueryRow(ctx, `
		SELECT event_type, aggregate_type, payload
		FROM outbox_events WHERE aggregate_id = $1
	`, reportID).Scan(&eventType, &aggregateType, &payload); err != nil {
		t.Fatalf("query outbox event: %v", err)
	}
	if eventType != "report.created" || aggregateType != "report" {
		t.Fatalf("event = (%q, %q), want report.created/report", eventType, aggregateType)
	}
	var payloadObject map[string]string
	if err := json.Unmarshal(payload, &payloadObject); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if len(payloadObject) != 1 || payloadObject["report_id"] != reportID {
		t.Fatalf("payload = %+v, want only report_id", payloadObject)
	}
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, argument string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, query, argument).Scan(&count); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != want {
		t.Fatalf("count = %d, want %d", count, want)
	}
}
