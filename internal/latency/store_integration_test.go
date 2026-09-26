//go:build integration

package latency

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

func TestTraceReportReconstructsCompletedDeliveryPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, cleanup := newLatencyTestPool(t, ctx)
	defer cleanup()

	base := time.Date(2026, 9, 26, 1, 0, 0, 0, time.UTC)
	reportID, incidentID, alertID, deliveryID, userID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO reports (id, raw_text, reporter_id, claimed_location, device_location, submitted_at, created_at) VALUES ($1, 'PRIVATE_REPORT_TEXT_SENTINEL', $2, ST_SetSRID(ST_MakePoint(3.37, 6.52), 4326)::geography, ST_SetSRID(ST_MakePoint(3.38, 6.53), 4326)::geography, $3, $3)`, reportID, userID, base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at, published_at) VALUES ('report.created', 'report', $1, '{}'::jsonb, $2, $3)`, reportID, base.Add(10*time.Millisecond), base.Add(110*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO report_ai_processing (report_id, state, started_at, completed_at, attempts, updated_at) VALUES ($1, 'SUCCEEDED', $2, $3, 1, $3)`, reportID, base.Add(120*time.Millisecond), base.Add(420*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, created_at, updated_at) VALUES ($1, 'OPEN', 'EMERGING', $2, $2)`, incidentID, base.Add(420*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id, attached_at) VALUES ($1, $2, $3)`, incidentID, reportID, base.Add(720*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint, created_at) VALUES ($1, $2, 'IMMEDIATE', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'PRIVATE_ALERT_TEXT_SENTINEL', 'test', '[]'::jsonb, $3, $4, 'fingerprint', $5)`, alertID, incidentID, base.Add(1020*time.Millisecond), uuid.NewString(), base.Add(1020*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO deliveries (id, alert_id, user_id, priority, channel, state, idempotency_key, payload, attempts, last_attempt_at, delivered_at, created_at, updated_at) VALUES ($1, $2, $3, 'P1', 'TEST', 'SUCCEEDED', $4, '{"push_endpoint":"PRIVATE_PUSH_ENDPOINT_SENTINEL"}'::jsonb, 1, $5, $6, $7, $6)`, deliveryID, alertID, userID, uuid.NewString(), base.Add(1120*time.Millisecond), base.Add(1170*time.Millisecond), base.Add(1070*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO delivery_attempts (delivery_id, attempt_no, operation_key, state, provider_response, started_at, completed_at) VALUES ($1, 1, 'safe-operation', 'SUCCEEDED', 'PRIVATE_PROVIDER_RESPONSE_SENTINEL', $2, $3)`, deliveryID, base.Add(1120*time.Millisecond), base.Add(1170*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	got, err := NewStore(pool).TraceReport(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IncidentID == nil || *got.IncidentID != incidentID.String() || got.AlertID == nil || *got.AlertID != alertID.String() || got.DeliveryID == nil || *got.DeliveryID != deliveryID.String() {
		t.Fatalf("correlation = report %q incident %v alert %v delivery %v", got.ReportID, got.IncidentID, got.AlertID, got.DeliveryID)
	}
	assertLatency(t, "outbox_publish_ms", got.OutboxPublishMs, 100)
	assertLatency(t, "ai_processing_ms", got.AIProcessingMs, 300)
	assertLatency(t, "incident_processing_ms", got.IncidentProcessingMs, 300)
	assertLatency(t, "delivery_queue_ms", got.DeliveryQueueMs, 50)
	assertLatency(t, "delivery_attempt_ms", got.DeliveryAttemptMs, 50)
	assertLatency(t, "report_to_alert_ms", got.ReportToAlertMs, 1020)
	assertLatency(t, "report_to_delivery_ms", got.ReportToDeliveryMs, 1170)
	if got.ReportPersistMs != nil || got.PriorityEvaluationMs != nil || got.AlertCreationMs != nil {
		t.Fatalf("unmeasurable stages fabricated durations: %+v", got)
	}
	encoded, err := marshalTimeline(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE_REPORT_TEXT_SENTINEL", "PRIVATE_ALERT_TEXT_SENTINEL", "PRIVATE_PUSH_ENDPOINT_SENTINEL", "PRIVATE_PROVIDER_RESPONSE_SENTINEL", "reporter_id", "claimed_location", "device_location"} {
		if contains(encoded, private) {
			t.Fatalf("timeline exposed %q: %s", private, encoded)
		}
	}
}

func TestTraceReportClassifiesOutboxAIAndDeliveryBacklogAndFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, cleanup := newLatencyTestPool(t, ctx)
	defer cleanup()
	base := time.Date(2026, 9, 26, 1, 0, 0, 0, time.UTC)
	reportID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO reports (id, raw_text, submitted_at, created_at) VALUES ($1, 'private', $2, $2)`, reportID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at, retry_attempts, last_error) VALUES ('report.created', 'report', $1, '{}'::jsonb, $2, 2, 'sensitive raw diagnostic content')`, reportID, base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO report_ai_processing (report_id, state, started_at, last_failed_at, failure_kind, attempts, updated_at) VALUES ($1, 'FAILED_RETRYABLE', $2, $3, 'transient', 1, $3)`, reportID, base.Add(2*time.Second), base.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}

	got, err := NewStore(pool).TraceReport(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage(StageOutboxPublish).State != StateFailedRetryable || got.Stage(StageAIProcessing).State != StateFailedRetryable {
		t.Fatalf("failure states = %s/%s", got.Stage(StageOutboxPublish).State, got.Stage(StageAIProcessing).State)
	}
	assertLatency(t, "ai_processing_ms", got.AIProcessingMs, 1000)
	if got.Stage(StageOutboxPublish).FailureKind != "publish_retryable" || got.Stage(StageAIProcessing).FailureKind != "transient" {
		t.Fatalf("safe failure kinds = %q/%q", got.Stage(StageOutboxPublish).FailureKind, got.Stage(StageAIProcessing).FailureKind)
	}
	encoded, err := marshalTimeline(got)
	if err != nil {
		t.Fatal(err)
	}
	if contains(encoded, "sensitive raw diagnostic content") {
		t.Fatalf("diagnostic exposed raw outbox error: %s", encoded)
	}
	if got.Stage(StageIncidentProcessing).State != StateUnavailable || got.Stage(StageAlertCreation).State != StateUnavailable {
		t.Fatalf("future stages = %s/%s, want unavailable", got.Stage(StageIncidentProcessing).State, got.Stage(StageAlertCreation).State)
	}
}

func TestTraceReportClassifiesDeliveryAttemptBacklogRetryAndQuarantine(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, cleanup := newLatencyTestPool(t, ctx)
	defer cleanup()
	base := time.Date(2026, 9, 26, 2, 0, 0, 0, time.UTC)
	cases := []struct {
		name, deliveryState, attemptState, failureKind string
		want                                           StageState
	}{
		{name: "pending", deliveryState: "IN_FLIGHT", attemptState: "STARTED", want: StatePending},
		{name: "retryable", deliveryState: "PENDING", attemptState: "FAILED", failureKind: "transient", want: StateFailedRetryable},
		{name: "quarantined", deliveryState: "QUARANTINED", attemptState: "QUARANTINED", failureKind: "permanent", want: StateFailedTerminal},
	}
	for index, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			reportID, incidentID, alertID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			created := base.Add(time.Duration(index) * time.Minute)
			userID := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO reports (id, raw_text, submitted_at, created_at) VALUES ($1, 'private', $2, $2)`, reportID, created); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, created_at, updated_at) VALUES ($1, 'OPEN', 'EMERGING', $2, $2)`, incidentID, created.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id, attached_at) VALUES ($1, $2, $3)`, incidentID, reportID, created.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint, created_at) VALUES ($1, $2, 'IMMEDIATE', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'private', 'test', '[]'::jsonb, $3, $4, 'fingerprint', $3)`, alertID, incidentID, created.Add(3*time.Second), uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO deliveries (id, alert_id, user_id, priority, channel, state, idempotency_key, payload, attempts, active_attempt_no, last_attempt_at, created_at, updated_at) VALUES ($1, $2, $3, 'P1', 'TEST', $4, $5, '{}'::jsonb, 1, CASE WHEN $4 = 'IN_FLIGHT' THEN 1 ELSE NULL END, $6, $6, $6)`, deliveryID, alertID, userID, test.deliveryState, uuid.NewString(), created.Add(4*time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO delivery_attempts (delivery_id, attempt_no, operation_key, state, failure_kind, started_at, completed_at) VALUES ($1, 1, 'safe-operation', $2, NULLIF($3, ''), $4, CASE WHEN $2 = 'STARTED' THEN NULL ELSE $5::timestamptz + interval '1 second' END)`, deliveryID, test.attemptState, test.failureKind, created.Add(5*time.Second), created.Add(5*time.Second)); err != nil {
				t.Fatal(err)
			}

			got, err := NewStore(pool).TraceReport(ctx, reportID)
			if err != nil {
				t.Fatal(err)
			}
			if state := got.Stage(StageDeliveryAttempt).State; state != test.want {
				t.Fatalf("delivery attempt state = %s, want %s", state, test.want)
			}
			if test.failureKind != "" && got.Stage(StageDeliveryAttempt).FailureKind != test.failureKind {
				t.Fatalf("safe failure kind = %q, want %q", got.Stage(StageDeliveryAttempt).FailureKind, test.failureKind)
			}
		})
	}
}

func TestTraceReportClassifiesAlertWithoutDeliveryAsPendingQueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, cleanup := newLatencyTestPool(t, ctx)
	defer cleanup()
	created := time.Date(2026, 9, 26, 3, 0, 0, 0, time.UTC)
	reportID, incidentID, alertID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO reports (id, raw_text, submitted_at, created_at) VALUES ($1, 'private', $2, $2)`, reportID, created); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, created_at, updated_at) VALUES ($1, 'OPEN', 'EMERGING', $2, $2)`, incidentID, created.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id, attached_at) VALUES ($1, $2, $3)`, incidentID, reportID, created.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint, created_at) VALUES ($1, $2, 'IMMEDIATE', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'private', 'test', '[]'::jsonb, $3, $4, 'fingerprint', $3)`, alertID, incidentID, created.Add(3*time.Second), uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	got, err := NewStore(pool).TraceReport(ctx, reportID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlertID == nil || *got.AlertID != alertID.String() || got.DeliveryID != nil {
		t.Fatalf("alert/delivery correlation = %v/%v, want alert and no delivery", got.AlertID, got.DeliveryID)
	}
	queue := got.Stage(StageDeliveryQueue)
	if queue.State != StatePending {
		t.Fatalf("delivery queue state = %s, want pending without a delivery", queue.State)
	}
	attempt := got.Stage(StageDeliveryAttempt)
	if attempt.State != StateUnavailable || attempt.AttemptCount != 0 || attempt.DurationMs != nil {
		t.Fatalf("absent delivery attempt = %+v, want unavailable with zero attempts and no duration", attempt)
	}
}

func TestRecentIsBoundedAndReturnsNewestReports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, cleanup := newLatencyTestPool(t, ctx)
	defer cleanup()
	base := time.Date(2026, 9, 26, 1, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		if _, err := pool.Exec(ctx, `INSERT INTO reports (raw_text, submitted_at, created_at) VALUES ('private', $1, $1)`, base.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := NewStore(pool).Recent(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Stage(StageReportPersist).CompletedAt.After(*got[1].Stage(StageReportPersist).CompletedAt) {
		t.Fatalf("Recent returned %d rows or wrong order: %+v", len(got), got)
	}
	if _, err := NewStore(pool).Recent(ctx, 101); err == nil {
		t.Fatal("Recent(101) error = nil, want bounded-limit error")
	}
}

func assertLatency(t *testing.T, name string, value *int64, want int64) {
	t.Helper()
	if value == nil || *value != want {
		t.Fatalf("%s = %v, want %d", name, value, want)
	}
}

func marshalTimeline(value Timeline) (string, error) {
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func newLatencyTestPool(t *testing.T, ctx context.Context) (*pgxpool.Pool, func()) {
	t.Helper()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("signa_latency_test_%d", time.Now().UnixNano())
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
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	goose := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", testURL.String(), "up")
	goose.Dir = root
	if output, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose up: %v\n%s", err, output)
	}
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	return pool, func() {
		pool.Close()
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	}
}
