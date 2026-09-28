//go:build integration

package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/geospatial"
	"github.com/onerandomd3v/signa/internal/incidents"
)

func TestPostgresTargetingEligibilityFreshnessDistanceCapAndPrivacy(t *testing.T) {
	ctx, pool, verifierService, _, incidentID := integrationStore(t)
	policy := testTargetingPolicy()
	policy.MaxCandidates = 2
	processor, err := NewTargetingProcessor(pool, policy, testPublicGeometryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	nearby := []struct {
		userID uuid.UUID
		lat    float64
		lon    float64
	}{
		{uuid.New(), 6.52439, 3.37921},
		{uuid.New(), 6.52431, 3.3792},
		{uuid.New(), 6.52421, 3.3792},
	}
	for _, candidate := range nearby {
		addTargetingCandidate(t, ctx, pool, candidate.userID, true, now, candidate.lat, candidate.lon)
	}
	ordinary := uuid.New()
	addTargetingCandidate(t, ctx, pool, ordinary, false, now, 6.5242, 3.3792)
	revoked := uuid.New()
	addTargetingCandidate(t, ctx, pool, revoked, true, now, 6.5242, 3.3792)
	if _, err := pool.Exec(ctx, `UPDATE user_capability_grants SET revoked_at=clock_timestamp(), revocation_provenance='operational_system' WHERE user_id=$1 AND revoked_at IS NULL`, revoked); err != nil {
		t.Fatal(err)
	}
	stale := uuid.New()
	addTargetingCandidate(t, ctx, pool, stale, true, now.Add(-time.Hour), 6.5242, 3.3792)
	outside := uuid.New()
	addTargetingCandidate(t, ctx, pool, outside, true, now, 6.54, 3.3792)
	future := uuid.New()
	addTargetingCandidate(t, ctx, pool, future, true, now.Add(time.Hour), 6.5242, 3.3792)

	message := targetingMessage(incidents.IncidentCreatedV1, incidentID)
	var beforeStatus, beforeConfidence string
	var beforeUpdated time.Time
	if err := pool.QueryRow(ctx, `SELECT status,confidence_state,updated_at FROM incidents WHERE id=$1`, incidentID).Scan(&beforeStatus, &beforeConfidence, &beforeUpdated); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	assigned := targetedVerifiers(t, ctx, pool, incidentID)
	want := []uuid.UUID{nearby[0].userID, nearby[1].userID}
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	if fmt.Sprint(assigned) != fmt.Sprint(want) {
		t.Fatalf("assigned=%v want=%v", assigned, want)
	}
	var storedPolicyVersion string
	var storedFingerprintLength int
	if err := pool.QueryRow(ctx, `SELECT targeting_policy_version,octet_length(targeting_policy_fingerprint) FROM verification_requests WHERE incident_id=$1 AND assigned_verifier_id=$2`, incidentID, nearby[0].userID).Scan(&storedPolicyVersion, &storedFingerprintLength); err != nil {
		t.Fatal(err)
	}
	if storedPolicyVersion != policy.Version || storedFingerprintLength != 32 {
		t.Fatalf("stored targeting policy=%q fingerprint length=%d", storedPolicyVersion, storedFingerprintLength)
	}
	if err := processor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	if got := targetedVerifiers(t, ctx, pool, incidentID); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("redelivery changed assignment set: %v", got)
	}
	var createdAt, expiresAt time.Time
	if err := pool.QueryRow(ctx, `SELECT created_at, expires_at FROM verification_requests WHERE incident_id=$1 AND assigned_verifier_id=$2`, incidentID, nearby[0].userID).Scan(&createdAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if !expiresAt.After(createdAt) || expiresAt.Sub(createdAt) > policy.RequestLifetime || expiresAt.Sub(createdAt) < policy.RequestLifetime-time.Second {
		t.Fatalf("request lifetime=%s, want %s", expiresAt.Sub(createdAt), policy.RequestLifetime)
	}
	views, err := verifierService.ListRequests(ctx, nearby[0].userID)
	if err != nil || len(views) != 2 {
		t.Fatalf("public views=%d err=%v", len(views), err)
	}
	encoded, err := json.Marshal(views)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{nearby[0].userID.String(), "3.37921", "6.52439", "verifier_id", "reporter_id", "raw_text", "center_point"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public verification output contains private value %q", private)
		}
	}
	var afterStatus, afterConfidence string
	var afterUpdated time.Time
	if err := pool.QueryRow(ctx, `SELECT status,confidence_state,updated_at FROM incidents WHERE id=$1`, incidentID).Scan(&afterStatus, &afterConfidence, &afterUpdated); err != nil {
		t.Fatal(err)
	}
	if afterStatus != beforeStatus || afterConfidence != beforeConfidence || !afterUpdated.Equal(beforeUpdated) {
		t.Fatalf("targeting changed incident lifecycle/confidence: before=%s/%s/%s after=%s/%s/%s", beforeStatus, beforeConfidence, beforeUpdated, afterStatus, afterConfidence, afterUpdated)
	}
}

func TestPostgresTargetingSkipsInactiveAndUnprojectableIncidents(t *testing.T) {
	ctx, pool, _, _, incidentID := integrationStore(t)
	processor, err := NewTargetingProcessor(pool, testTargetingPolicy(), testPublicGeometryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	addTargetingCandidate(t, ctx, pool, user, true, time.Now(), 6.5242, 3.3792)
	if _, err := pool.Exec(ctx, `UPDATE incidents SET status='RESOLVED' WHERE id=$1`, incidentID); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, targetingMessage(incidents.IncidentCreatedV1, incidentID)); err != nil {
		t.Fatal(err)
	}
	if countTargetRequests(t, ctx, pool, incidentID) != 0 {
		t.Fatal("resolved incident was targeted")
	}
	privateIncident := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents(id,event_type,status,confidence_state) VALUES ($1,'hidden','OPEN','UNVERIFIED')`, privateIncident); err != nil {
		t.Fatal(err)
	}
	if err := processor.Process(ctx, targetingMessage(incidents.IncidentCreatedV1, privateIncident)); err != nil {
		t.Fatal(err)
	}
	if countTargetRequests(t, ctx, pool, privateIncident) != 0 {
		t.Fatal("incident outside public projection was targeted")
	}
}

func TestPostgresConcurrentTargetingRespectsCapAndUniqueness(t *testing.T) {
	ctx, pool, _, _, incidentID := integrationStore(t)
	policy := testTargetingPolicy()
	policy.MaxCandidates = 3
	first, err := NewTargetingProcessor(pool, policy, testPublicGeometryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewTargetingProcessor(pool, policy, testPublicGeometryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		addTargetingCandidate(t, ctx, pool, uuid.New(), true, now, 6.5242+float64(i)*0.00001, 3.3792)
	}
	message := targetingMessage(incidents.IncidentCreatedV1, incidentID)
	errs := make(chan error, 2)
	for _, processor := range []*TargetingProcessor{first, second} {
		go func(processor *TargetingProcessor) { errs <- processor.Process(ctx, message) }(processor)
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := countTargetRequests(t, ctx, pool, incidentID); got != policy.MaxCandidates {
		t.Fatalf("targeted count=%d want=%d", got, policy.MaxCandidates)
	}
	var duplicates int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT incident_id, assigned_verifier_id, targeting_policy_version, targeting_policy_fingerprint FROM verification_requests WHERE incident_id=$1 AND assigned_verifier_id IS NOT NULL GROUP BY 1,2,3,4 HAVING count(*)>1) duplicates`, incidentID).Scan(&duplicates); err != nil {
		t.Fatal(err)
	}
	if duplicates != 0 {
		t.Fatalf("duplicate targeting tuples=%d", duplicates)
	}
	changedPolicy := policy
	changedPolicy.RadiusMeters++
	changedProcessor, err := NewTargetingProcessor(pool, changedPolicy, testPublicGeometryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := changedProcessor.Process(ctx, message); err != nil {
		t.Fatal(err)
	}
	if got := countTargetRequests(t, ctx, pool, incidentID); got != policy.MaxCandidates {
		t.Fatalf("policy change exceeded active per-incident candidate cap: %d", got)
	}
}

func TestPostgresTargetingRespectsLocationDeletion(t *testing.T) {
	ctx, pool, _, _, incidentID := integrationStore(t)
	processor, err := NewTargetingProcessor(pool, testTargetingPolicy(), testPublicGeometryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	addTargetingCandidate(t, ctx, pool, user, true, time.Now(), 6.5242, 3.3792)
	geoStore, err := geospatial.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := geoStore.DeleteUserLocation(ctx, user, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var deletionRecords int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_location_deletions WHERE user_id=$1`, user).Scan(&deletionRecords); err != nil {
		t.Fatal(err)
	}
	if deletionRecords != 1 {
		t.Fatalf("deletion records=%d", deletionRecords)
	}
	if err := processor.Process(ctx, targetingMessage(incidents.IncidentCreatedV1, incidentID)); err != nil {
		t.Fatal(err)
	}
	if countTargetRequests(t, ctx, pool, incidentID) != 0 {
		t.Fatal("deleted location snapshot was used for targeting")
	}
}

func addTargetingCandidate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, trusted bool, observedAt time.Time, latitude, longitude float64) {
	t.Helper()
	if trusted {
		if _, err := pool.Exec(ctx, `INSERT INTO user_capability_grants(user_id,capability,grant_provenance) VALUES ($1,'trusted_verifier','operational_system')`, userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_locations(user_id,location,observed_at) VALUES ($1,ST_SetSRID(ST_MakePoint($3,$2),4326)::geography,$4)`, userID, latitude, longitude, observedAt); err != nil {
		t.Fatal(err)
	}
}

func targetingMessage(name string, incidentID uuid.UUID) incidents.StreamMessage {
	return incidents.StreamMessage{Values: map[string]any{
		"event_name": name, "aggregate_type": "incident",
		"payload": `{"incident_id":"` + incidentID.String() + `"}`,
	}}
}

func targetedVerifiers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, incidentID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT assigned_verifier_id FROM verification_requests WHERE incident_id=$1 AND assigned_verifier_id IS NOT NULL`, incidentID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}

func countTargetRequests(t *testing.T, ctx context.Context, pool *pgxpool.Pool, incidentID uuid.UUID) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM verification_requests WHERE incident_id=$1 AND assigned_verifier_id IS NOT NULL`, incidentID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
