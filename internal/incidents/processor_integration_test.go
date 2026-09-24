//go:build integration

package incidents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/similarity"
	"github.com/onerandomd3v/signa/internal/config"
)

func TestIncidentProcessorCreateAttachAndConcurrencyIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = integrationDatabaseURL
	}
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
	processor := newIntegrationProcessor(t, pool)
	lat, lon := 6.5244, 3.3792
	observed := time.Now().UTC().Add(-5 * time.Minute)

	createReport := uuid.New()
	createExtraction := uuid.New()
	createResult := integrationExtraction("road_blockage", "Market Junction", "five minutes ago")
	insertIntegrationReport(t, ctx, pool, createReport, "road blocked near market junction", &lat, &lon, nil, observed)
	insertIntegrationExtraction(t, ctx, pool, createExtraction, createReport, createResult)
	processIntegrationMessage(t, ctx, processor, createReport, createExtraction)

	var incidentID string
	var status, confidence string
	var severity *string
	if err := pool.QueryRow(ctx, `SELECT id::text, status, confidence_state, severity FROM incidents WHERE id = (SELECT incident_id FROM reports WHERE id = $1)`, createReport).Scan(&incidentID, &status, &confidence, &severity); err != nil {
		t.Fatal(err)
	}
	if status != "OPEN" || confidence != "UNVERIFIED" || severity != nil {
		t.Fatalf("bootstrap = %s/%s/%v", status, confidence, severity)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_reports WHERE incident_id = $1 AND report_id = $2`, 1, incidentID, createReport)
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, 1, incidentID)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type = 'incident.created' AND aggregate_id = $1`, 1, incidentID)

	// A device-only report creates an incident without copying private device coordinates.
	deviceReport, deviceExtraction := uuid.New(), uuid.New()
	deviceLat, deviceLon := 6.6, 3.5
	insertIntegrationReport(t, ctx, pool, deviceReport, "flooding reported", nil, nil, &deviceLocation{deviceLat, deviceLon}, observed)
	insertIntegrationExtraction(t, ctx, pool, deviceExtraction, deviceReport, integrationExtraction("flooding", "", "now"))
	processIntegrationMessage(t, ctx, processor, deviceReport, deviceExtraction)
	var centerIsNull bool
	if err := pool.QueryRow(ctx, `SELECT center_point IS NULL FROM incidents WHERE id = (SELECT incident_id FROM reports WHERE id = $1)`, deviceReport).Scan(&centerIsNull); err != nil {
		t.Fatal(err)
	}
	if !centerIsNull {
		t.Fatal("device-only report seeded incident center_point")
	}

	// Two concurrent CREATE decisions for a fresh eligible path produce one incident.
	concurrentResult := integrationExtraction("road_blockage_concurrent", "Market Junction", "five minutes ago")
	concurrentReports := [][2]uuid.UUID{{uuid.New(), uuid.New()}, {uuid.New(), uuid.New()}}
	for _, pair := range concurrentReports {
		insertIntegrationReport(t, ctx, pool, pair[0], "road blocked near market junction", &lat, &lon, nil, observed)
		insertIntegrationExtraction(t, ctx, pool, pair[1], pair[0], concurrentResult)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(concurrentReports))
	for _, pair := range concurrentReports {
		wg.Add(1)
		go func(reportID, extractionID uuid.UUID) {
			defer wg.Done()
			errs <- processor.Process(ctx, integrationMessage(reportID, extractionID))
		}(pair[0], pair[1])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var concurrentIncidentID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM incidents WHERE event_type = 'road_blockage_concurrent'`).Scan(&concurrentIncidentID); err != nil {
		t.Fatal(err)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM incidents WHERE event_type = 'road_blockage_concurrent'`, 1)
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_reports WHERE incident_id = $1`, 2, concurrentIncidentID)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type IN ('incident.created', 'incident.report_attached') AND aggregate_id = $1`, 2, concurrentIncidentID)

	// Redelivery after commit is a no-op.
	processIntegrationMessage(t, ctx, processor, createReport, createExtraction)
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_reports WHERE report_id = $1`, 1, createReport)
}

func TestIncidentProcessorCreateAndAttachAtomicityIntegration(t *testing.T) {
	for _, mode := range []string{"create", "attach"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
			if databaseURL == "" {
				databaseURL = integrationDatabaseURL
			}
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
			processor := newIntegrationProcessor(t, pool)
			lat, lon := 6.5244, 3.3792
			observed := time.Now().UTC().Add(-5 * time.Minute)
			if mode == "create" {
				report, extractionID := uuid.New(), uuid.New()
				insertIntegrationReport(t, ctx, pool, report, "road blocked", &lat, &lon, nil, observed)
				insertIntegrationExtraction(t, ctx, pool, extractionID, report, integrationExtraction("atomic_create", "Market Junction", "five minutes ago"))
				if _, err := pool.Exec(ctx, `DROP TABLE incident_state_history`); err != nil {
					t.Fatal(err)
				}
				if err := processor.Process(ctx, integrationMessage(report, extractionID)); err == nil {
					t.Fatal("create unexpectedly succeeded after history failure")
				}
				assertCount(t, ctx, pool, `SELECT count(*) FROM incidents`, 0)
				assertCount(t, ctx, pool, `SELECT count(*) FROM incident_reports`, 0)
				assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events`, 0)
				assertCount(t, ctx, pool, `SELECT count(*) FROM reports WHERE incident_id IS NOT NULL`, 0)
				return
			}

			candidate := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, event_type, status, confidence_state, center_point, last_signal_at, created_at, updated_at) VALUES ($1, 'atomic_attach', 'OPEN', 'UNVERIFIED', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, $4, $4, $4)`, candidate, lon, lat, observed); err != nil {
				t.Fatal(err)
			}
			candidateReport, candidateExtraction := uuid.New(), uuid.New()
			insertIntegrationReport(t, ctx, pool, candidateReport, "road blocked", &lat, &lon, nil, observed)
			insertIntegrationExtraction(t, ctx, pool, candidateExtraction, candidateReport, integrationExtraction("atomic_attach", "Market Junction", "five minutes ago"))
			if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, candidate, candidateReport); err != nil {
				t.Fatal(err)
			}
			report, extractionID := uuid.New(), uuid.New()
			insertIntegrationReport(t, ctx, pool, report, "road blocked", &lat, &lon, nil, observed)
			insertIntegrationExtraction(t, ctx, pool, extractionID, report, integrationExtraction("atomic_attach", "Market Junction", "five minutes ago"))
			if _, err := pool.Exec(ctx, `DROP TABLE incident_state_history`); err != nil {
				t.Fatal(err)
			}
			if err := processor.Process(ctx, integrationMessage(report, extractionID)); err == nil {
				t.Fatal("attach unexpectedly succeeded after history failure")
			}
			assertCount(t, ctx, pool, `SELECT count(*) FROM incidents`, 1)
			assertCount(t, ctx, pool, `SELECT count(*) FROM incident_reports WHERE report_id = $1`, 0, report)
			assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events`, 0)
			assertCount(t, ctx, pool, `SELECT count(*) FROM reports WHERE incident_id IS NOT NULL AND id = $1`, 0, report)
		})
	}
}

func TestIncidentProcessorReevaluatesResolvedOrExpiredCandidateIntegration(t *testing.T) {
	for _, mode := range []string{"resolved", "expired"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
			if databaseURL == "" {
				databaseURL = integrationDatabaseURL
			}
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
			candidate := uuid.New()
			candidateReport, candidateExtraction := uuid.New(), uuid.New()
			lat, lon := 6.5244, 3.3792
			observed := time.Now().UTC().Add(-5 * time.Minute)
			if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, event_type, status, confidence_state, center_point, last_signal_at, created_at, updated_at) VALUES ($1, 'road_blockage', 'OPEN', 'UNVERIFIED', ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography, $4, $4, $4)`, candidate, lon, lat, observed); err != nil {
				t.Fatal(err)
			}
			insertIntegrationReport(t, ctx, pool, candidateReport, "road blocked near market junction", &lat, &lon, nil, observed)
			candidateResult := integrationExtraction("road_blockage", "Market Junction", "five minutes ago")
			insertIntegrationExtraction(t, ctx, pool, candidateExtraction, candidateReport, candidateResult)
			if _, err := pool.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, candidate, candidateReport); err != nil {
				t.Fatal(err)
			}
			report, result := uuid.New(), integrationExtraction("road_blockage", "Market Junction", "five minutes ago")
			extractionID := uuid.New()
			insertIntegrationReport(t, ctx, pool, report, "road blocked near market junction", &lat, &lon, nil, observed)
			insertIntegrationExtraction(t, ctx, pool, extractionID, report, result)
			validator := integrationSimilarityValidator(t)
			base := similarity.Processor{Provider: similarity.RuleBasedScorer{}, Validator: validator}
			assessor := &mutatingAssessor{pool: pool, candidate: candidate, mode: mode, delegate: base}
			processor := NewProcessor(pool, NewStore(pool), assessor, validator, integrationPolicy())
			processIntegrationMessage(t, ctx, processor, report, extractionID)
			var linked string
			if err := pool.QueryRow(ctx, `SELECT incident_id::text FROM reports WHERE id = $1`, report).Scan(&linked); err != nil {
				t.Fatal(err)
			}
			if linked == candidate.String() {
				t.Fatal("report attached to resolved/expired candidate")
			}
			assertCount(t, ctx, pool, `SELECT count(*) FROM incidents WHERE id = $1`, 1, linked)
			processIntegrationMessage(t, ctx, processor, report, extractionID)
			assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE aggregate_id = $1`, 1, linked)
		})
	}
}

func newIntegrationProcessor(t *testing.T, pool *pgxpool.Pool) *Processor {
	validator := integrationSimilarityValidator(t)
	return NewProcessor(pool, NewStore(pool), similarity.Processor{Provider: similarity.RuleBasedScorer{}, Validator: validator}, validator, integrationPolicy())
}
func integrationPolicy() config.IncidentPolicy {
	return config.IncidentPolicy{CandidateRadiusMeters: 1000, CandidateTimeWindow: 2 * time.Hour, CandidateLimit: 10, SimilarityThreshold: .5, SimilarityWinnerMargin: .1}
}
func integrationSimilarityValidator(t *testing.T) *similarity.Validator {
	_, file, _, _ := runtime.Caller(0)
	schema, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "contracts", "ai", "similarity", "v1", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	validator, err := similarity.NewValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	return validator
}
func integrationExtraction(eventType, place, timeText string) extraction.Extraction {
	var placeValue *string
	locationStatus := "identified"
	if place == "" {
		locationStatus = "unknown"
	} else {
		placeValue = &place
	}
	return extraction.Extraction{ContractVersion: similarity.InputContractVersion, TaxonomyVersion: "signa.event-taxonomy.v0", EventType: extraction.Field{Status: "identified", Value: &eventType, Candidates: []string{}, EvidenceQuotes: []string{"report"}}, LocationReference: extraction.Field{Status: locationStatus, Value: placeValue, Candidates: []string{}, EvidenceQuotes: []string{}}, TimeReference: extraction.Field{Status: "identified", Value: &timeText, Candidates: []string{}, EvidenceQuotes: []string{"report"}}}
}

type deviceLocation struct{ lat, lon float64 }

func insertIntegrationReport(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, text string, lat, lon *float64, device *deviceLocation, observed time.Time) {
	t.Helper()
	var dlat, dlon *float64
	if device != nil {
		dlat, dlon = &device.lat, &device.lon
	}
	_, err := pool.Exec(ctx, `INSERT INTO reports (id, raw_text, claimed_location, device_location, observed_at, submitted_at) VALUES ($1, $2, CASE WHEN $3::double precision IS NULL OR $4::double precision IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint($4, $3), 4326)::geography END, CASE WHEN $5::double precision IS NULL OR $6::double precision IS NULL THEN NULL ELSE ST_SetSRID(ST_MakePoint($6, $5), 4326)::geography END, $7, $7)`, id, text, lat, lon, dlat, dlon, observed)
	if err != nil {
		t.Fatal(err)
	}
}
func insertIntegrationExtraction(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, reportID uuid.UUID, result extraction.Extraction) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO report_ai_extractions (id, report_id, contract_version, taxonomy_version, result) VALUES ($1, $2, $3, $4, $5::jsonb)`, id, reportID, result.ContractVersion, result.TaxonomyVersion, encoded)
	if err != nil {
		t.Fatal(err)
	}
}
func integrationMessage(reportID, extractionID uuid.UUID) StreamMessage {
	payload, _ := json.Marshal(map[string]string{"report_id": reportID.String(), "extraction_id": extractionID.String(), "contract_version": similarity.InputContractVersion})
	return StreamMessage{ID: uuid.NewString(), Values: map[string]any{"event_name": ReportAIProcessedV1, "aggregate_type": "report", "payload": string(payload)}}
}
func processIntegrationMessage(t *testing.T, ctx context.Context, processor *Processor, reportID, extractionID uuid.UUID) {
	t.Helper()
	if err := processor.Process(ctx, integrationMessage(reportID, extractionID)); err != nil {
		t.Fatal(err)
	}
}
func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}

type mutatingAssessor struct {
	pool      *pgxpool.Pool
	candidate uuid.UUID
	mode      string
	delegate  SimilarityAssessor
	once      sync.Once
}

func (a *mutatingAssessor) Assess(ctx context.Context, report similarity.ReportEvidence, candidate similarity.CandidateIncident) ([]byte, error) {
	result, err := a.delegate.Assess(ctx, report, candidate)
	if err != nil {
		return nil, err
	}
	if candidate.ID == a.candidate.String() {
		a.once.Do(func() {
			if a.mode == "resolved" {
				_, _ = a.pool.Exec(ctx, `UPDATE incidents SET resolved_at = now() WHERE id = $1`, a.candidate)
			} else {
				_, _ = a.pool.Exec(ctx, `UPDATE incidents SET expires_at = now() WHERE id = $1`, a.candidate)
			}
		})
	}
	return result, nil
}
