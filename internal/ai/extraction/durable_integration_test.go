//go:build integration

package extraction

import (
	"context"
	"encoding/json"
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

func TestDurableExtractionPersistenceIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, name := createExtractionDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	}()
	runExtractionGoose(t, ctx, testURL, "up")
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	reportID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO reports (id, raw_text) VALUES ($1, 'report text')`, reportID); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	schema, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "extraction", "v0", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{result: []byte(validExtractionJSON())}
	acker := &fakeAcker{}
	processor := NewProcessor(NewPostgresReportReader(pool), provider, validator, NewDurableStore(pool, validator), acker, "signa:report-events", "integration")
	message := StreamMessage{ID: "message-1", Values: map[string]any{"event_id": "event-1", "event_name": ReportCreatedV1, "aggregate_type": "report", "payload": fmt.Sprintf(`{"report_id":"%s"}`, reportID)}}
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 after durable redelivery", provider.calls)
	}
	if acker.calls != 2 {
		t.Fatalf("acks = %d, want 2", acker.calls)
	}
	var extractionCount, eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM report_ai_extractions WHERE report_id = $1`, reportID).Scan(&extractionCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type = 'report.ai_processed' AND aggregate_id = $1`, reportID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if extractionCount != 1 || eventCount != 1 {
		t.Fatalf("durable rows = extraction %d, events %d", extractionCount, eventCount)
	}
	var payload map[string]any
	var encoded []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE event_type = 'report.ai_processed' AND aggregate_id = $1`, reportID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"report_id", "extraction_id", "contract_version"} {
		if payload[key] == nil {
			t.Fatalf("event payload missing %s: %s", key, encoded)
		}
	}
}

func TestDurableExtractionFailureDoesNotAckOrPersist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, name := createExtractionDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	}()
	runExtractionGoose(t, ctx, testURL, "up")
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reportID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO reports (id, raw_text) VALUES ($1, 'report text')`, reportID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE outbox_events`); err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{result: []byte(validExtractionJSON())}
	acker := &fakeAcker{}
	validator := &fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0", TaxonomyVersion: "signa.event-taxonomy.v0"}}
	processor := NewProcessor(NewPostgresReportReader(pool), provider, validator, NewDurableStore(pool, validator), acker, "signa:report-events", "integration")
	err = processor.Process(ctx, StreamMessage{ID: "message-1", Values: map[string]any{"event_id": "event-1", "event_name": ReportCreatedV1, "aggregate_type": "report", "payload": fmt.Sprintf(`{"report_id":"%s"}`, reportID)}})
	if err == nil || acker.calls != 0 {
		t.Fatalf("Process() err = %v, acks = %d; want failure without ack", err, acker.calls)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM report_ai_extractions WHERE report_id = $1`, reportID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("extraction rows after failed outbox commit = %d, want 0", count)
	}
}

func createExtractionDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("signa_extraction_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		_ = maintenance.Close(context.Background())
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + name
	return testURL.String(), maintenance, name
}

func runExtractionGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cmd := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}
