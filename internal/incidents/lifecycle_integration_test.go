//go:build integration

package incidents

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/incidents/lifecycle"
)

func TestLifecycleProcessorEventsSweepIdempotencyAndPrivacyIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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

	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	policy := lifecycle.Policy{ResolvingAfter: time.Hour, ResolvedAfter: 2 * time.Hour, ExpiredAfter: 3 * time.Hour}
	processor, err := NewLifecycleProcessor(pool, policy)
	if err != nil {
		t.Fatal(err)
	}
	current := base.Add(30 * time.Minute)
	processor.now = func() time.Time { return current }
	incidentID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, event_type, status, confidence_state, center_point, started_at, last_signal_at, created_at, updated_at) VALUES ($1, 'road_blockage', 'OPEN', 'HIGH_CONFIDENCE', ST_SetSRID(ST_MakePoint(3.3792, 6.5244), 4326)::geography, $2, $2, $2, $2)`, incidentID, base); err != nil {
		t.Fatal(err)
	}
	message := lifecycleMessage(IncidentCreatedV1, incidentID)
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	assertLifecycleState(t, ctx, pool, incidentID, lifecycle.Open, base.Add(3*time.Hour), nil)
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, 0, incidentID)

	current = base.Add(time.Hour)
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	assertLifecycleState(t, ctx, pool, incidentID, lifecycle.Resolving, base.Add(3*time.Hour), nil)
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1 AND changed_field = 'status'`, 1, incidentID)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type = 'incident.status_changed' AND aggregate_id = $1`, 1, incidentID)

	// Duplicate event processing is an idempotent no-op.
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, 1, incidentID)

	current = base.Add(2 * time.Hour)
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	assertLifecycleState(t, ctx, pool, incidentID, lifecycle.Resolved, base.Add(3*time.Hour), timePtr(base.Add(2*time.Hour)))
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type = 'incident.resolved' AND aggregate_id = $1`, 1, incidentID)
	if candidates, err := NewStore(pool).LookupCandidates(ctx, CandidateLookup{EventType: "road_blockage", Latitude: 6.5244, Longitude: 3.3792, RadiusMeters: 1000, AsOf: base.Add(2 * time.Hour), TimeWindow: 4 * time.Hour, Limit: 10}); err != nil {
		t.Fatal(err)
	} else if len(candidates) != 0 {
		t.Fatalf("resolved incident remained a candidate: %+v", candidates)
	}

	current = base.Add(3 * time.Hour)
	if err := processor.Sweep(ctx, 10); err != nil {
		t.Fatal(err)
	}
	assertLifecycleState(t, ctx, pool, incidentID, lifecycle.Expired, base.Add(3*time.Hour), timePtr(base.Add(2*time.Hour)))
	var confidenceState string
	var severity *string
	if err := pool.QueryRow(ctx, `SELECT confidence_state, severity FROM incidents WHERE id = $1`, incidentID).Scan(&confidenceState, &severity); err != nil {
		t.Fatal(err)
	}
	if confidenceState != "HIGH_CONFIDENCE" || severity != nil {
		t.Fatalf("lifecycle changed confidence/severity = %s/%v", confidenceState, severity)
	}
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, 3, incidentID)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type = 'incident.status_changed' AND aggregate_id = $1`, 3, incidentID)
	assertCount(t, ctx, pool, `SELECT count(*) FROM outbox_events WHERE event_type = 'incident.resolved' AND aggregate_id = $1`, 1, incidentID)

	rows, err := pool.Query(ctx, `SELECT payload FROM outbox_events WHERE aggregate_id = $1`, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		payloadText := string(payload)
		for _, secret := range []string{"road blockage", "reporter", "6.5244", "3.3792"} {
			if strings.Contains(payloadText, secret) {
				t.Fatalf("private lifecycle payload contains %q: %s", secret, payloadText)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// Multiple workers sweeping the same due incident produce one transition.
	concurrentID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, started_at, last_signal_at, created_at, updated_at) VALUES ($1, 'OPEN', 'UNVERIFIED', $2, $2, $2, $2)`, concurrentID, base); err != nil {
		t.Fatal(err)
	}
	current = base.Add(time.Hour)
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- processor.Sweep(ctx, 10)
		}()
	}
	wg.Wait()
	close(errs)
	for sweepErr := range errs {
		if sweepErr != nil {
			t.Fatal(sweepErr)
		}
	}
	assertLifecycleState(t, ctx, pool, concurrentID, lifecycle.Resolving, base.Add(3*time.Hour), nil)
	assertCount(t, ctx, pool, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, 1, concurrentID)

	// A fresh signal reopens RESOLVING but never reopens terminal states.
	refreshID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, started_at, last_signal_at, created_at, updated_at) VALUES ($1, 'RESOLVING', 'UNVERIFIED', $2, $2, $2, $2)`, refreshID, base); err != nil {
		t.Fatal(err)
	}
	current = base.Add(30 * time.Minute)
	if _, err := pool.Exec(ctx, `UPDATE incidents SET last_signal_at = $2 WHERE id = $1`, refreshID, current); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, lifecycleMessage(IncidentReportAttachedV1, refreshID)); err != nil {
		t.Fatal(err)
	}
	assertLifecycleState(t, ctx, pool, refreshID, lifecycle.Open, current.Add(3*time.Hour), nil)
}

func TestLifecycleProcessorAtomicityIntegration(t *testing.T) {
	for _, table := range []string{"incident_state_history", "outbox_events"} {
		t.Run(table, func(t *testing.T) {
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
			base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
			incidentID := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, started_at, last_signal_at, created_at, updated_at) VALUES ($1, 'OPEN', 'UNVERIFIED', $2, $2, $2, $2)`, incidentID, base); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `DROP TABLE `+table); err != nil {
				t.Fatal(err)
			}
			processor, err := NewLifecycleProcessor(pool, lifecycle.Policy{time.Hour, 2 * time.Hour, 3 * time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			processor.now = func() time.Time { return base.Add(time.Hour) }
			if err := processor.Process(ctx, lifecycleMessage(IncidentCreatedV1, incidentID)); err == nil {
				t.Fatal("lifecycle transition unexpectedly succeeded after dependency failure")
			}
			var status string
			var expiresAt *time.Time
			if err := pool.QueryRow(ctx, `SELECT status, expires_at FROM incidents WHERE id = $1`, incidentID).Scan(&status, &expiresAt); err != nil {
				t.Fatal(err)
			}
			if status != lifecycle.Open || expiresAt != nil {
				t.Fatalf("rolled-back lifecycle state = %s/%v", status, expiresAt)
			}
		})
	}
}

func lifecycleMessage(name string, incidentID uuid.UUID) StreamMessage {
	payload, _ := json.Marshal(map[string]string{"incident_id": incidentID.String()})
	return StreamMessage{ID: uuid.NewString(), Values: map[string]any{"event_name": name, "aggregate_type": "incident", "payload": string(payload)}}
}

func assertLifecycleState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, incidentID uuid.UUID, status string, expiresAt time.Time, resolvedAt *time.Time) {
	t.Helper()
	var gotStatus string
	var gotExpires, gotResolved *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, expires_at, resolved_at FROM incidents WHERE id = $1`, incidentID).Scan(&gotStatus, &gotExpires, &gotResolved); err != nil {
		t.Fatal(err)
	}
	if gotStatus != status || gotExpires == nil || !gotExpires.Equal(expiresAt) || !sameLifecycleTime(gotResolved, resolvedAt) {
		t.Fatalf("incident lifecycle = %s/%v/%v, want %s/%s/%v", gotStatus, gotExpires, gotResolved, status, expiresAt, resolvedAt)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
