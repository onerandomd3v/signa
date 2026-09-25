//go:build integration

package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReadAuthorizedUsesDeliveryVisibilityAndPreservesSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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

	incidentID, alertID := uuid.New(), uuid.New()
	visibleUserID, hiddenUserID := uuid.New(), uuid.New()
	asOf := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, severity) VALUES ($1, 'RESOLVED', 'DISPUTED', 'HIGH')`, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, supersedes_alert_id, idempotency_key, request_fingerprint) VALUES ($1, $2, 'IMMEDIATE', 'DISPUTED', 'HIGH', 'RESOLVED', 'P1', 'STALE', 'historical safe summary', 'signa.alert-eligibility.v1', '[]', $3, NULL, 'read-alert-key', 'read-alert-fingerprint')`, alertID, incidentID, asOf); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id, userID, state string
	}{
		{uuid.NewString(), visibleUserID.String(), "SUCCEEDED"},
		{uuid.NewString(), hiddenUserID.String(), "SKIPPED"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO deliveries (id, alert_id, user_id, priority, channel, state, idempotency_key, payload) VALUES ($1, $2, $3, 'P1', 'TEST', $4, $5, '{}'::jsonb)`, item.id, alertID, item.userID, item.state, "delivery-"+item.id); err != nil {
			t.Fatal(err)
		}
	}

	read, err := NewStore(pool).ReadAuthorized(ctx, visibleUserID, alertID)
	if err != nil {
		t.Fatal(err)
	}
	if read.StatusSnapshot != "RESOLVED" || read.FreshnessSnapshot != FreshnessStale || read.Message != "historical safe summary" || !read.AsOf.Equal(asOf) {
		t.Fatalf("immutable snapshot changed: %+v", read)
	}
	if _, err := NewStore(pool).ReadAuthorized(ctx, hiddenUserID, alertID); !errors.Is(err, ErrAlertNotVisible) {
		t.Fatalf("hidden user error = %v, want ErrAlertNotVisible", err)
	}
	if _, err := NewStore(pool).ReadAuthorized(ctx, uuid.New(), alertID); !errors.Is(err, ErrAlertNotVisible) {
		t.Fatalf("cross-user error = %v, want ErrAlertNotVisible", err)
	}
}
