//go:build integration

package incidents

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/incidents/confidence"
)

func TestEvidencePolicyTransitionsIdempotencyAndPrivacyIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	databaseURL := integrationDatabaseURL
	testURL, maintenance, name := createIntegrationDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	}()
	runGoose(t, ctx, testURL, "up")
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	processor, err := NewEvidencePolicyProcessor(pool, confidence.Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	incidentID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state) VALUES ($1, 'OPEN', 'UNVERIFIED')`, incidentID); err != nil {
		t.Fatal(err)
	}
	firstReport, firstExtraction := uuid.New(), uuid.New()
	firstReporter, secondReporter := uuid.New(), uuid.New()
	insertConfidenceReport(t, ctx, pool, firstReport, &firstReporter, "private raw first report")
	insertIntegrationExtraction(t, ctx, pool, firstExtraction, firstReport, confidenceExtraction("LOW"))
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, firstReport); err != nil {
		t.Fatal(err)
	}
	createdMessage := confidenceMessage(IncidentCreatedV1, incidentID)
	if err := processor.Process(ctx, createdMessage); err != nil {
		t.Fatal(err)
	}
	assertIncidentState(t, ctx, pool, incidentID, "EMERGING", "LOW")
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type IN ('incident.confidence_changed', 'incident.severity_changed') AND aggregate_id = $1`, 2, incidentID)

	secondReport, secondExtraction := uuid.New(), uuid.New()
	insertConfidenceReport(t, ctx, pool, secondReport, &secondReporter, "a distinct second report")
	insertIntegrationExtraction(t, ctx, pool, secondExtraction, secondReport, confidenceExtraction("HIGH"))
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, secondReport); err != nil {
		t.Fatal(err)
	}
	attachedMessage := confidenceMessage(IncidentReportAttachedV1, incidentID)
	if err := processor.Process(ctx, attachedMessage); err != nil {
		t.Fatal(err)
	}
	assertIncidentState(t, ctx, pool, incidentID, "CORROBORATED", "HIGH")
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type IN ('incident.confidence_changed', 'incident.severity_changed') AND aggregate_id = $1`, 4, incidentID)

	// A distinct report from the first reporter is not an independent source.
	sameReporterReport, sameReporterExtraction := uuid.New(), uuid.New()
	insertConfidenceReport(t, ctx, pool, sameReporterReport, &firstReporter, "a different wording from the first reporter")
	insertIntegrationExtraction(t, ctx, pool, sameReporterExtraction, sameReporterReport, confidenceExtraction("LOW"))
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, sameReporterReport); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, attachedMessage); err != nil {
		t.Fatal(err)
	}
	assertIncidentState(t, ctx, pool, incidentID, "CORROBORATED", "HIGH")
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type IN ('incident.confidence_changed', 'incident.severity_changed') AND aggregate_id = $1`, 4, incidentID)

	// An unknown/ambiguous candidate does not clear an existing severity.
	thirdReport, thirdExtraction := uuid.New(), uuid.New()
	insertConfidenceReport(t, ctx, pool, thirdReport, nil, "an uncertain report")
	unknown := confidenceExtraction("")
	unknown.SeverityCandidate = extraction.Field{Status: "ambiguous", Candidates: []string{"LOW", "CRITICAL"}, EvidenceQuotes: []string{}}
	insertIntegrationExtraction(t, ctx, pool, thirdExtraction, thirdReport, unknown)
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, thirdReport); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, attachedMessage); err != nil {
		t.Fatal(err)
	}
	assertIncidentState(t, ctx, pool, incidentID, "CORROBORATED", "HIGH")
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type IN ('incident.confidence_changed', 'incident.severity_changed') AND aggregate_id = $1`, 4, incidentID)

	thirdReporter, fourthReport, fourthExtraction := uuid.New(), uuid.New(), uuid.New()
	insertConfidenceReport(t, ctx, pool, fourthReport, &thirdReporter, "a genuinely independent third report")
	insertIntegrationExtraction(t, ctx, pool, fourthExtraction, fourthReport, confidenceExtraction("MODERATE"))
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, fourthReport); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, attachedMessage); err != nil {
		t.Fatal(err)
	}
	assertIncidentState(t, ctx, pool, incidentID, "HIGH_CONFIDENCE", "HIGH")
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type IN ('incident.confidence_changed', 'incident.severity_changed') AND aggregate_id = $1`, 5, incidentID)

	// Commit-before-ACK redelivery and concurrent evaluation are no-ops.
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- processor.Process(ctx, attachedMessage)
		}()
	}
	wg.Wait()
	close(errs)
	for processErr := range errs {
		if processErr != nil {
			t.Fatal(processErr)
		}
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type IN ('incident.confidence_changed', 'incident.severity_changed') AND aggregate_id = $1`, 5, incidentID)

	var payloads [][]byte
	rows, err := pool.Query(ctx, `SELECT payload FROM outbox_events WHERE aggregate_id = $1`, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, payload)
	}
	rows.Close()
	for _, payload := range payloads {
		if strings.Contains(string(payload), "private raw first report") || strings.Contains(string(payload), "6.5244") {
			t.Fatalf("sensitive data in outbox payload: %s", payload)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"incident_id", "changed_field", "new_value", "policy_version", "metadata"} {
			if decoded[key] == nil {
				t.Fatalf("payload missing %s: %s", key, payload)
			}
		}
	}
}

func TestEvidencePolicyRollbackLeavesStateAndEventsUnchangedIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	testURL, maintenance, name := createIntegrationDatabase(t, ctx, integrationDatabaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	}()
	runGoose(t, ctx, testURL, "up")
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	processor, err := NewEvidencePolicyProcessor(pool, confidence.Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	incidentID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state) VALUES ($1, 'OPEN', 'UNVERIFIED')`, incidentID); err != nil {
		t.Fatal(err)
	}
	reportID, extractionID := uuid.New(), uuid.New()
	insertConfidenceReport(t, ctx, pool, reportID, nil, "rollback private report")
	insertIntegrationExtraction(t, ctx, pool, extractionID, reportID, confidenceExtraction("CRITICAL"))
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, reportID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE incident_state_history`); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, confidenceMessage(IncidentCreatedV1, incidentID)); err == nil {
		t.Fatal("evaluation unexpectedly succeeded after history table failure")
	}
	assertIncidentState(t, ctx, pool, incidentID, "UNVERIFIED", "")
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, 0, incidentID)
}

func TestEvidencePolicyOutboxRollbackLeavesStateAndHistoryUnchangedIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	testURL, maintenance, name := createIntegrationDatabase(t, ctx, integrationDatabaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	}()
	runGoose(t, ctx, testURL, "up")
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	processor, err := NewEvidencePolicyProcessor(pool, confidence.Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	incidentID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state) VALUES ($1, 'OPEN', 'UNVERIFIED')`, incidentID); err != nil {
		t.Fatal(err)
	}
	reportID, extractionID := uuid.New(), uuid.New()
	insertConfidenceReport(t, ctx, pool, reportID, nil, "outbox rollback report")
	insertIntegrationExtraction(t, ctx, pool, extractionID, reportID, confidenceExtraction("HIGH"))
	if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, reportID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE outbox_events`); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, confidenceMessage(IncidentCreatedV1, incidentID)); err == nil {
		t.Fatal("evaluation unexpectedly succeeded after outbox table failure")
	}
	assertIncidentState(t, ctx, pool, incidentID, "UNVERIFIED", "")
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, 0, incidentID)
}

func confidenceMessage(eventName string, incidentID uuid.UUID) StreamMessage {
	payload, _ := json.Marshal(map[string]string{"incident_id": incidentID.String()})
	return StreamMessage{ID: uuid.NewString(), Values: map[string]any{"event_name": eventName, "aggregate_type": "incident", "payload": string(payload)}}
}

func confidenceExtraction(severity string) extraction.Extraction {
	return extraction.Extraction{
		ContractVersion:   "signa.ai.report-extraction.v0",
		TaxonomyVersion:   "signa.event-taxonomy.v0",
		EventType:         extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}},
		LocationReference: extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}},
		TimeReference:     extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}},
		SourceClaim:       extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}},
		Language:          extraction.Field{Status: "identified", Value: stringPtr("en"), Candidates: []string{}, EvidenceQuotes: []string{}},
		SeverityCandidate: extraction.Field{Status: "identified", Value: stringPtr(severity), Candidates: []string{}, EvidenceQuotes: []string{}},
	}
}

func insertConfidenceReport(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reportID uuid.UUID, reporterID *uuid.UUID, rawText string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO reports (id, reporter_id, raw_text, submitted_at) VALUES ($1, $2, $3, now())`, reportID, reporterID, rawText); err != nil {
		t.Fatal(err)
	}
}

func stringPtr(value string) *string { return &value }

func assertIncidentState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, incidentID uuid.UUID, confidenceState, severity string) {
	t.Helper()
	var gotConfidence string
	var gotSeverity *string
	if err := pool.QueryRow(ctx, `SELECT confidence_state, severity FROM incidents WHERE id = $1`, incidentID).Scan(&gotConfidence, &gotSeverity); err != nil {
		t.Fatal(err)
	}
	if gotConfidence != confidenceState {
		t.Fatalf("confidence = %q, want %q", gotConfidence, confidenceState)
	}
	if severity == "" {
		if gotSeverity != nil {
			t.Fatalf("severity = %q, want NULL", *gotSeverity)
		}
		return
	}
	if gotSeverity == nil || *gotSeverity != severity {
		t.Fatalf("severity = %v, want %q", gotSeverity, severity)
	}
}
