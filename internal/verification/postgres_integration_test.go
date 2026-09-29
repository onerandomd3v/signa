//go:build integration

package verification

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

func integrationStore(t *testing.T) (context.Context, *pgxpool.Pool, Service, uuid.UUID, uuid.UUID) {
	return integrationStoreWithMaxConns(t, 6)
}

func integrationStoreWithMaxConns(t *testing.T, maxConns int32) (context.Context, *pgxpool.Pool, Service, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Skipf("PostgreSQL test service unavailable: %v", err)
	}
	name := fmt.Sprintf("signa_verification_service_%d", time.Now().UnixNano())
	if _, err = maintenance.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	})
	testURL := *parsed
	testURL.Path = "/" + name
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	cmd := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", testURL.String(), "up")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("goose up: %v\n%s", err, output)
	}
	poolConfig, err := pgxpool.ParseConfig(testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	policy := config.PublicIncidentGeometryPolicy{Version: config.PublicIncidentGeometryPolicyVersion, GridMeters: 100, MinRadiusMeters: 250, SimplifyMeters: 25}
	service := NewService(pool, incidents.NewStore(pool), policy)
	userID, incidentID := uuid.New(), uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO user_capability_grants(user_id,capability,grant_provenance) VALUES ($1,'trusted_verifier','operational_system')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO incidents(id,event_type,status,confidence_state,center_point) VALUES ($1,'road_blockage','OPEN','UNVERIFIED',ST_SetSRID(ST_MakePoint(3.3792,6.5244),4326)::geography)`, incidentID); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, service, userID, incidentID
}

func TestPostgresSubmitWithOneConnection(t *testing.T) {
	ctx, pool, service, user, incident := integrationStoreWithMaxConns(t, 1)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	conclusion := ConclusionConfirm
	response, created, err := service.SubmitResponse(ctx, user, request, "one-connection", ResponseInput{Conclusion: &conclusion})
	if err != nil || !created || response.RequestID != request || response.IncidentID != incident {
		t.Fatalf("response=%+v created=%v error=%v", response, created, err)
	}
	if countResponses(t, ctx, pool, request) != 1 {
		t.Fatal("response was not persisted")
	}
}
func addRequest(t *testing.T, ctx context.Context, pool *pgxpool.Pool, incidentID uuid.UUID, assigned *uuid.UUID, expires time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO verification_requests(id,incident_id,assigned_verifier_id,expires_at) VALUES($1,$2,$3,$4)`, id, incidentID, assigned, expires); err != nil {
		t.Fatal(err)
	}
	return id
}
func countResponses(t *testing.T, ctx context.Context, pool *pgxpool.Pool, requestID uuid.UUID) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM verification_responses WHERE request_id=$1`, requestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestPostgresActiveAndRevokedVerifierGrant(t *testing.T) {
	ctx, pool, service, user, _ := integrationStore(t)
	if _, err := service.ListRequests(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE user_capability_grants SET revoked_at=clock_timestamp(),revocation_provenance='operational_system' WHERE user_id=$1 AND revoked_at IS NULL`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListRequests(ctx, user); !errors.Is(err, ErrNotTrustedVerifier) {
		t.Fatalf("revoked error=%v", err)
	}
}
func TestPostgresOperationalGrantProvenance(t *testing.T) {
	ctx, pool, _, user, _ := integrationStore(t)
	var actor *uuid.UUID
	var provenance string
	if err := pool.QueryRow(ctx, `SELECT granted_by,grant_provenance FROM user_capability_grants WHERE user_id=$1`, user).Scan(&actor, &provenance); err != nil {
		t.Fatal(err)
	}
	if actor != nil || provenance != "operational_system" {
		t.Fatalf("actor=%v provenance=%s", actor, provenance)
	}
}
func TestPostgresRequestAudienceExpiryAndCancellation(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	valid := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	expired := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE verification_requests SET created_at=clock_timestamp()-interval '1 hour', expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, expired); err != nil {
		t.Fatal(err)
	}
	atBoundary := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	if _, err := pool.Exec(ctx, `UPDATE verification_requests SET expires_at=clock_timestamp() WHERE id=$1`, atBoundary); err != nil {
		t.Fatal(err)
	}
	assigned := uuid.New()
	other := addRequest(t, ctx, pool, incident, &assigned, time.Now().Add(time.Hour))
	cancelled := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	_, _ = pool.Exec(ctx, `UPDATE verification_requests SET cancelled_at=clock_timestamp() WHERE id=$1`, cancelled)
	if _, err := service.GetRequest(ctx, user, valid); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{expired, atBoundary, other, cancelled} {
		if _, err := service.GetRequest(ctx, user, id); !errors.Is(err, ErrRequestNotFound) {
			t.Fatalf("id=%s error=%v", id, err)
		}
	}
}

func TestPostgresRequestProjectionMissIsNotFound(t *testing.T) {
	ctx, pool, service, user, _ := integrationStore(t)
	privateIncident := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents(id,event_type,status,confidence_state) VALUES ($1,'hidden','OPEN','UNVERIFIED')`, privateIncident); err != nil {
		t.Fatal(err)
	}
	request := addRequest(t, ctx, pool, privateIncident, nil, time.Now().Add(time.Hour))
	views, err := service.ListRequests(ctx, user)
	if err != nil || len(views) != 0 {
		t.Fatalf("views=%v error=%v", views, err)
	}
	if _, err := service.GetRequest(ctx, user, request); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("detail error=%v", err)
	}
	conclusion := ConclusionConfirm
	if _, _, err := service.SubmitResponse(ctx, user, request, "hidden", ResponseInput{Conclusion: &conclusion}); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("submit error=%v", err)
	}
	if countResponses(t, ctx, pool, request) != 0 {
		t.Fatal("hidden incident accepted a response")
	}
}
func TestPostgresListRequestsIsBoundedAndDeterministic(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	base := time.Now().Add(-time.Hour)
	ids := make([]uuid.UUID, 0, 103)
	for i := 0; i < 103; i++ {
		id := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
		ids = append(ids, id)
		if _, err := pool.Exec(ctx, `UPDATE verification_requests SET created_at=$2 WHERE id=$1`, id, base); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() > ids[j].String() })
	views, err := service.ListRequests(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 100 {
		t.Fatalf("count=%d", len(views))
	}
	for i, view := range views {
		if view.ID != ids[i] {
			t.Fatalf("order at %d: %s != %s", i, view.ID, ids[i])
		}
	}
}

func TestPostgresListEligibleUsesAssignedAndUnassignedBranches(t *testing.T) {
	ctx, pool, _, verifierID, incidentID := integrationStore(t)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	type expectedRequest struct {
		id        uuid.UUID
		createdAt time.Time
		assigned  bool
	}
	expected := make([]expectedRequest, 0, 130)
	for i := 0; i < 130; i++ {
		createdAt := base.Add(time.Duration(i/2) * time.Second)
		var assigned *uuid.UUID
		isAssigned := i%2 == 1
		if isAssigned {
			assigned = &verifierID
		}
		id := addRequest(t, ctx, pool, incidentID, assigned, time.Now().Add(time.Hour))
		if _, err := pool.Exec(ctx, `UPDATE verification_requests SET created_at=$2 WHERE id=$1`, id, createdAt); err != nil {
			t.Fatal(err)
		}
		expected = append(expected, expectedRequest{id: id, createdAt: createdAt, assigned: isAssigned})
	}

	otherVerifier := uuid.New()
	other := addRequest(t, ctx, pool, incidentID, &otherVerifier, time.Now().Add(time.Hour))
	expired := addRequest(t, ctx, pool, incidentID, &verifierID, time.Now().Add(time.Hour))
	cancelled := addRequest(t, ctx, pool, incidentID, nil, time.Now().Add(time.Hour))
	for _, id := range []uuid.UUID{other, cancelled} {
		if _, err := pool.Exec(ctx, `UPDATE verification_requests SET created_at=$2 WHERE id=$1`, id, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE verification_requests SET created_at=$2, expires_at=$3 WHERE id=$1`, expired, base.Add(1000*time.Second), time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE verification_requests SET cancelled_at=clock_timestamp() WHERE id=$1`, cancelled); err != nil {
		t.Fatal(err)
	}

	sort.Slice(expected, func(i, j int) bool {
		if expected[i].createdAt.Equal(expected[j].createdAt) {
			return expected[i].id.String() > expected[j].id.String()
		}
		return expected[i].createdAt.After(expected[j].createdAt)
	})
	got, err := (&postgresRepository{pool: pool}).ListEligible(ctx, verifierID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxRequestListLimit {
		t.Fatalf("ListEligible returned %d requests, want %d", len(got), maxRequestListLimit)
	}
	assignedCount, unassignedCount := 0, 0
	for i, request := range got {
		if request.ID != expected[i].id || !request.CreatedAt.Equal(expected[i].createdAt) {
			t.Fatalf("request[%d]=(%s,%s), want (%s,%s)", i, request.ID, request.CreatedAt, expected[i].id, expected[i].createdAt)
		}
		if expected[i].assigned {
			assignedCount++
		} else {
			unassignedCount++
		}
		if request.ID == other || request.ID == expired || request.ID == cancelled {
			t.Fatalf("ineligible request %s was returned", request.ID)
		}
	}
	if assignedCount == 0 || unassignedCount == 0 {
		t.Fatalf("result branches: assigned=%d unassigned=%d; want both", assignedCount, unassignedCount)
	}
}

func TestPostgresConcurrentIdempotentSubmissions(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	conclusion := ConclusionConfirm
	input := ResponseInput{Conclusion: &conclusion}
	const n = 8
	var wg sync.WaitGroup
	type result struct {
		response Response
		created  bool
		err      error
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, created, err := service.SubmitResponse(ctx, user, request, "retry", input)
			results <- result{response, created, err}
		}()
	}
	wg.Wait()
	close(results)
	var first uuid.UUID
	createdCount := 0
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		if first == uuid.Nil {
			first = item.response.ID
		}
		if item.response.ID != first {
			t.Fatalf("response IDs differ")
		}
		if item.created {
			createdCount++
		}
	}
	if createdCount != 1 || countResponses(t, ctx, pool, request) != 1 {
		t.Fatalf("created=%d", createdCount)
	}
	version, digest, err := fingerprintResponse(input)
	if err != nil {
		t.Fatal(err)
	}
	var storedVersion int16
	var storedDigest []byte
	if err := pool.QueryRow(ctx, `SELECT payload_fingerprint_version,payload_fingerprint FROM verification_responses WHERE request_id=$1`, request).Scan(&storedVersion, &storedDigest); err != nil {
		t.Fatal(err)
	}
	if storedVersion != version || string(storedDigest) != string(digest[:]) {
		t.Fatal("stored fingerprint differs")
	}
}
func TestPostgresConcurrentConflictingIdempotencySubmissions(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	a, b := ConclusionConfirm, ConclusionDispute
	var wg sync.WaitGroup
	type result struct {
		input    ResponseInput
		response Response
		created  bool
		err      error
	}
	results := make(chan result, 2)
	for _, c := range []Conclusion{a, b} {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			input := ResponseInput{Conclusion: &c}
			response, created, err := service.SubmitResponse(ctx, user, request, "conflict", input)
			results <- result{input, response, created, err}
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	var winner result
	for item := range results {
		if item.err == nil {
			success++
			winner = item
			if !item.created {
				t.Fatal("successful caller did not create response")
			}
		} else if errors.Is(item.err, ErrIdempotencyConflict) {
			conflict++
		} else {
			t.Fatal(item.err)
		}
	}
	if success != 1 || conflict != 1 || countResponses(t, ctx, pool, request) != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	version, digest, err := fingerprintResponse(winner.input)
	if err != nil {
		t.Fatal(err)
	}
	var storedID uuid.UUID
	var conclusion *string
	var observation *string
	var storedVersion int16
	var storedDigest []byte
	if err := pool.QueryRow(ctx, `SELECT id,conclusion,observation,payload_fingerprint_version,payload_fingerprint FROM verification_responses WHERE request_id=$1`, request).Scan(&storedID, &conclusion, &observation, &storedVersion, &storedDigest); err != nil {
		t.Fatal(err)
	}
	if storedID != winner.response.ID || conclusion == nil || *conclusion != string(*winner.input.Conclusion) || observation != nil || storedVersion != version || string(storedDigest) != string(digest[:]) {
		t.Fatalf("stored response does not match winner: id=%s conclusion=%v observation=%v version=%d digest=%x", storedID, conclusion, observation, storedVersion, storedDigest)
	}
}

func TestPostgresSubmitRejectsIncidentChangedWhileWaitingForRequestLock(t *testing.T) {
	ctx, pool, service, user, originalIncident := integrationStore(t)
	request := addRequest(t, ctx, pool, originalIncident, nil, time.Now().Add(time.Hour))
	otherIncident := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents(id,event_type,status,confidence_state,center_point) VALUES ($1,'other','OPEN','UNVERIFIED',ST_SetSRID(ST_MakePoint(3.3792,6.5244),4326)::geography)`, otherIncident); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var blockerPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE verification_requests SET incident_id=$2 WHERE id=$1`, request, otherIncident); err != nil {
		t.Fatal(err)
	}
	conclusion := ConclusionConfirm
	result := make(chan error, 1)
	go func() {
		_, _, err := service.SubmitResponse(ctx, user, request, "changed", ResponseInput{Conclusion: &conclusion})
		result <- err
	}()
	waitForBlockedSubmit(t, ctx, pool, blockerPID, "verification_requests")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("changed incident submit error=%v", err)
	}
	if countResponses(t, ctx, pool, request) != 0 {
		t.Fatal("response persisted against unchecked incident")
	}
}

func TestPostgresSubmitRejectsIncidentLeavingPublicProjectionWhileWaiting(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var blockerPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE verification_requests SET created_at=created_at WHERE id=$1`, request); err != nil {
		t.Fatal(err)
	}
	conclusion := ConclusionConfirm
	result := make(chan error, 1)
	go func() {
		_, _, err := service.SubmitResponse(ctx, user, request, "no-longer-public", ResponseInput{Conclusion: &conclusion})
		result <- err
	}()
	waitForBlockedSubmit(t, ctx, pool, blockerPID, "verification_requests")
	if _, err := pool.Exec(ctx, `UPDATE incidents SET status='RESOLVED' WHERE id=$1`, incident); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("now-private incident submit error=%v", err)
	}
	if countResponses(t, ctx, pool, request) != 0 {
		t.Fatal("response persisted for incident absent from public projection")
	}
}
func TestPostgresIdempotencyConflictDoesNotOverwrite(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	a, b := ConclusionConfirm, ConclusionDispute
	original, _, err := service.SubmitResponse(ctx, user, request, "key", ResponseInput{Conclusion: &a})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.SubmitResponse(ctx, user, request, "key", ResponseInput{Conclusion: &b})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error=%v", err)
	}
	replay, created, err := service.SubmitResponse(ctx, user, request, "key", ResponseInput{Conclusion: &a})
	if err != nil || created || replay.ID != original.ID || countResponses(t, ctx, pool, request) != 1 {
		t.Fatalf("replay=%+v created=%v err=%v", replay, created, err)
	}
}
func TestPostgresDistinctKeysAppendResponses(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	c := ConclusionConfirm
	first, created, err := service.SubmitResponse(ctx, user, request, "one", ResponseInput{Conclusion: &c})
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	second, created, err := service.SubmitResponse(ctx, user, request, "two", ResponseInput{Conclusion: &c})
	if err != nil || !created || first.ID == second.ID || countResponses(t, ctx, pool, request) != 2 {
		t.Fatalf("second: %v %v", created, err)
	}
}
func TestPostgresRestrictiveForeignKeysPreserveAuditRows(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	c := ConclusionConfirm
	if _, _, err := service.SubmitResponse(ctx, user, request, "key", ResponseInput{Conclusion: &c}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM verification_requests WHERE id=$1`, request); err == nil {
		t.Fatal("request with response was deleted")
	}
	if countResponses(t, ctx, pool, request) != 1 {
		t.Fatal("response lost")
	}
}
func TestPostgresResponseDoesNotChangeIncidentConfidence(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	c := ConclusionConfirm
	if _, _, err := service.SubmitResponse(ctx, user, request, "key", ResponseInput{Conclusion: &c}); err != nil {
		t.Fatal(err)
	}
	var confidence string
	if err := pool.QueryRow(ctx, `SELECT confidence_state FROM incidents WHERE id=$1`, incident).Scan(&confidence); err != nil {
		t.Fatal(err)
	}
	if confidence != "UNVERIFIED" {
		t.Fatalf("confidence=%s", confidence)
	}
}
func TestPostgresRevocationSerializesWithResponseSubmission(t *testing.T) {
	ctx, pool, service, user, incident := integrationStore(t)
	request := addRequest(t, ctx, pool, incident, nil, time.Now().Add(time.Hour))
	c := ConclusionConfirm
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var blockerPID int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE user_capability_grants SET revoked_at=clock_timestamp(),revocation_provenance='operational_system' WHERE user_id=$1 AND revoked_at IS NULL`, user); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := service.SubmitResponse(ctx, user, request, "key", ResponseInput{Conclusion: &c})
		result <- err
	}()
	waitForBlockedSubmit(t, ctx, pool, blockerPID, "user_capability_grants")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrNotTrustedVerifier) {
		t.Fatalf("submit after revocation=%v", err)
	}
	if countResponses(t, ctx, pool, request) != 0 {
		t.Fatal("response accepted after revocation")
	}
}

func waitForBlockedSubmit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blockerPID int, table string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND $1=ANY(pg_blocking_pids(pid)) AND position($2 in query)>0 AND position('FOR UPDATE' in query)>0)`, blockerPID, table).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("submit did not wait on %s row lock held by pid %d", table, blockerPID)
}
