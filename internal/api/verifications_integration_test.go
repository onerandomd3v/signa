//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/verification"
)

func verificationIntegrationSetup(t *testing.T) (context.Context, *pgxpool.Pool, http.Handler, uuid.UUID, uuid.UUID, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	_ = conn.Close(ctx)
	testURL, maintenance, name := createMediaIntegrationDatabase(t, ctx, databaseURL)
	t.Cleanup(func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name)
		_ = maintenance.Close(context.Background())
	})
	runMediaGoose(t, ctx, testURL, "up")
	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	user, ordinary, incident := uuid.New(), uuid.New(), uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO user_capability_grants(user_id,capability,grant_provenance) VALUES ($1,'trusted_verifier','operational_system')`, user)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO incidents(id,event_type,status,confidence_state,center_point) VALUES ($1,'road_blockage','OPEN','UNVERIFIED',ST_SetSRID(ST_MakePoint(3.3792,6.5244),4326)::geography)`, incident)
	if err != nil {
		t.Fatal(err)
	}
	requestID := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO verification_requests(id,incident_id,assigned_verifier_id,expires_at) VALUES ($1,$2,$3,clock_timestamp()+interval '1 hour')`, requestID, incident, user)
	if err != nil {
		t.Fatal(err)
	}
	sessionStore := auth.NewPostgresStore(pool)
	userSession, err := sessionStore.Create(ctx, user, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ordinarySession, err := sessionStore.Create(ctx, ordinary, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	policy := config.PublicIncidentGeometryPolicy{Version: config.PublicIncidentGeometryPolicyVersion, GridMeters: 100, MinRadiusMeters: 250, SimplifyMeters: 25}
	public := incidents.NewStore(pool)
	handler := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, pool, nil, public, policy, AuthConfig{Store: sessionStore, Verification: verification.NewService(pool, public, policy)})
	return ctx, pool, handler, user, requestID, userSession.Secret, ordinarySession.Secret
}

func TestVerificationRoutesHideIncidentExcludedFromPublicProjection(t *testing.T) {
	ctx, pool, handler, user, _, cookie, _ := verificationIntegrationSetup(t)
	privateIncident := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO incidents(id,event_type,status,confidence_state) VALUES ($1,'private','OPEN','UNVERIFIED')`, privateIncident)
	if err != nil {
		t.Fatal(err)
	}
	hidden := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO verification_requests(id,incident_id,assigned_verifier_id,expires_at) VALUES ($1,$2,$3,clock_timestamp()+interval '1 hour')`, hidden, privateIncident, user)
	if err != nil {
		t.Fatal(err)
	}
	list := verificationCallWithCookie(handler, "GET", "/v1/verifications/requests", "", cookie, "")
	if list.Code != 200 || strings.Contains(list.Body.String(), hidden.String()) {
		t.Fatalf("list=%d %s", list.Code, list.Body.String())
	}
	hiddenPath := "/v1/verifications/requests/" + hidden.String()
	missingPath := "/v1/verifications/requests/" + uuid.NewString()
	hiddenDetail := verificationCallWithCookie(handler, "GET", hiddenPath, "", cookie, "")
	missingDetail := verificationCallWithCookie(handler, "GET", missingPath, "", cookie, "")
	if hiddenDetail.Code != 404 || hiddenDetail.Body.String() != missingDetail.Body.String() {
		t.Fatalf("detail hidden=%d %s missing=%d %s", hiddenDetail.Code, hiddenDetail.Body.String(), missingDetail.Code, missingDetail.Body.String())
	}
	hiddenSubmit := verificationCallWithCookie(handler, "POST", hiddenPath+"/responses", `{"conclusion":"CONFIRM"}`, cookie, "key")
	missingSubmit := verificationCallWithCookie(handler, "POST", missingPath+"/responses", `{"conclusion":"CONFIRM"}`, cookie, "key")
	if hiddenSubmit.Code != 404 || hiddenSubmit.Body.String() != missingSubmit.Body.String() {
		t.Fatalf("submit hidden=%d %s missing=%d %s", hiddenSubmit.Code, hiddenSubmit.Body.String(), missingSubmit.Code, missingSubmit.Body.String())
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM verification_responses WHERE request_id=$1`, hidden).Scan(&count); err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestVerificationRoutesPostgresBoundary(t *testing.T) {
	ctx, pool, handler, user, requestID, cookie, ordinary := verificationIntegrationSetup(t)
	path := "/v1/verifications/requests/" + requestID.String()
	noCookie := verificationCallWithCookie(handler, "GET", path, "", "", "")
	assertVerificationError(t, noCookie, 401, "unauthenticated", "authentication is required")
	ordinaryRead := verificationCallWithCookie(handler, "GET", path, "", ordinary, "")
	assertVerificationError(t, ordinaryRead, 403, "forbidden", "trusted verifier access is required")
	ordinarySubmit := verificationCallWithCookie(handler, "POST", path+"/responses", `{"observation":"SAW"}`, ordinary, "key")
	assertVerificationError(t, ordinarySubmit, 403, "forbidden", "trusted verifier access is required")
	detail := verificationCallWithCookie(handler, "GET", path, "", cookie, "")
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), verification.SafetyPrompt) {
		t.Fatalf("detail=%d %s", detail.Code, detail.Body.String())
	}
	for _, private := range []string{"reporter_id", "assigned_verifier_id", "verifier_id", "fingerprint", "idempotency_key", "raw_text", "center_point"} {
		if strings.Contains(detail.Body.String(), private) {
			t.Fatalf("private field %s in detail", private)
		}
	}
	foreign := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO user_capability_grants(user_id,capability,grant_provenance) VALUES ($1,'trusted_verifier','operational_system')`, foreign)
	if err != nil {
		t.Fatal(err)
	}
	foreignSession, err := auth.NewPostgresStore(pool).Create(ctx, foreign, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cross := verificationCallWithCookie(handler, "GET", path, "", foreignSession.Secret, "")
	missing := verificationCallWithCookie(handler, "GET", "/v1/verifications/requests/"+uuid.NewString(), "", foreignSession.Secret, "")
	if cross.Code != 404 || cross.Body.String() != missing.Body.String() {
		t.Fatalf("cross=%d %s missing=%d %s", cross.Code, cross.Body.String(), missing.Code, missing.Body.String())
	}
	posted := verificationCallWithCookie(handler, "POST", path+"/responses", `{"conclusion":null,"observation":"HEARD"}`, cookie, "durable-key")
	if posted.Code != 201 {
		t.Fatalf("posted=%d %s", posted.Code, posted.Body.String())
	}
	replay := verificationCallWithCookie(handler, "POST", path+"/responses", `{"conclusion":null,"observation":"HEARD"}`, cookie, "durable-key")
	if replay.Code != 200 || replay.Body.String() != posted.Body.String() {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	conflict := verificationCallWithCookie(handler, "POST", path+"/responses", `{"conclusion":"DISPUTE"}`, cookie, "durable-key")
	assertVerificationError(t, conflict, 409, "idempotency_conflict", "Idempotency-Key was already used for a different response")
	var result map[string]any
	if err := json.Unmarshal(posted.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 6 || result["conclusion"] != nil || result["observation"] != "HEARD" {
		t.Fatalf("response=%v", result)
	}
	var storedUser uuid.UUID
	var count int
	err = pool.QueryRow(ctx, `SELECT verifier_id FROM verification_responses WHERE request_id=$1`, requestID).Scan(&storedUser)
	if err != nil || storedUser != user {
		t.Fatalf("stored user=%s err=%v", storedUser, err)
	}
	err = pool.QueryRow(ctx, `SELECT count(*) FROM verification_responses WHERE request_id=$1`, requestID).Scan(&count)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	var confidence string
	err = pool.QueryRow(ctx, `SELECT confidence_state FROM incidents WHERE id=(SELECT incident_id FROM verification_requests WHERE id=$1)`, requestID).Scan(&confidence)
	if err != nil || confidence != "UNVERIFIED" {
		t.Fatalf("confidence=%s err=%v", confidence, err)
	}
	pool.Close()
	policy := config.PublicIncidentGeometryPolicy{Version: config.PublicIncidentGeometryPolicyVersion, GridMeters: 100, MinRadiusMeters: 250, SimplifyMeters: 25}
	outage := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, policy, AuthConfig{
		Store:        sessionStoreForAPITest{secret: "outage-cookie", principal: auth.Principal{UserID: user}},
		Verification: verification.NewService(pool, incidents.NewStore(pool), policy),
	})
	assertVerificationError(t, verificationCallWithCookie(outage, "GET", "/v1/verifications/requests", "", "outage-cookie", ""), 503, "verification_unavailable", "verification service is unavailable")
}

func verificationCallWithCookie(handler http.Handler, method, path, body, cookie, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "signa_session", Value: cookie})
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
