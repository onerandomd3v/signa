//go:build integration

package migrations

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestVerificationMigrationCreatesAuditableSchema(t *testing.T) {
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL URL: %v", err)
	}
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Skipf("PostgreSQL test service unavailable: %v", err)
	}
	defer func() { _ = maintenance.Close(context.Background()) }()
	databaseName := fmt.Sprintf("signa_verification_migration_test_%d", time.Now().UnixNano())
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+databaseName); err != nil {
		t.Fatalf("create isolated database: %v", err)
	}
	defer func() {
		if _, err := maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
	}()
	testURL := *parsed
	testURL.Path = "/" + databaseName
	runGoose(t, ctx, testURL.String(), "up")
	connection, err := pgx.Connect(ctx, testURL.String())
	if err != nil {
		t.Fatalf("connect to isolated database: %v", err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	for _, table := range []string{"user_capability_grants", "verification_requests", "verification_responses"} {
		assertTableExists(t, ctx, connection, "public", table)
	}

	t.Run("columns and canonical UUID identity", func(t *testing.T) {
		want := map[string]struct{ dataType, nullable string }{
			"user_capability_grants.id": {"uuid", "NO"}, "user_capability_grants.user_id": {"uuid", "NO"},
			"user_capability_grants.capability": {"text", "NO"}, "user_capability_grants.granted_at": {"timestamp with time zone", "NO"},
			"user_capability_grants.granted_by": {"uuid", "YES"}, "user_capability_grants.grant_provenance": {"text", "NO"},
			"user_capability_grants.revoked_at": {"timestamp with time zone", "YES"}, "user_capability_grants.revoked_by": {"uuid", "YES"},
			"user_capability_grants.revocation_provenance": {"text", "YES"},
			"verification_requests.id":                     {"uuid", "NO"}, "verification_requests.incident_id": {"uuid", "NO"},
			"verification_requests.assigned_verifier_id": {"uuid", "YES"}, "verification_requests.created_at": {"timestamp with time zone", "NO"},
			"verification_requests.expires_at": {"timestamp with time zone", "NO"}, "verification_requests.cancelled_at": {"timestamp with time zone", "YES"},
			"verification_requests.targeting_policy_version":     {"text", "YES"},
			"verification_requests.targeting_policy_fingerprint": {"bytea", "YES"},
			"verification_responses.id":                          {"uuid", "NO"}, "verification_responses.request_id": {"uuid", "NO"},
			"verification_responses.incident_id": {"uuid", "NO"}, "verification_responses.verifier_id": {"uuid", "NO"},
			"verification_responses.conclusion": {"text", "YES"}, "verification_responses.observation": {"text", "YES"},
			"verification_responses.payload_fingerprint_version": {"smallint", "NO"},
			"verification_responses.payload_fingerprint":         {"bytea", "NO"},
			"verification_responses.idempotency_key":             {"character varying", "NO"},
			"verification_responses.created_at":                  {"timestamp with time zone", "NO"},
		}
		rows, err := connection.Query(ctx, `SELECT table_name, column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = 'public' AND table_name IN ('user_capability_grants', 'verification_requests', 'verification_responses')`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var table, column, dataType, nullable string
			if err := rows.Scan(&table, &column, &dataType, &nullable); err != nil {
				t.Fatal(err)
			}
			key := table + "." + column
			expected, ok := want[key]
			if !ok {
				continue
			}
			if dataType != expected.dataType || nullable != expected.nullable {
				t.Errorf("%s = (%s, %s), want (%s, %s)", key, dataType, nullable, expected.dataType, expected.nullable)
			}
			delete(want, key)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		for key := range want {
			t.Errorf("missing column %s", key)
		}
		var usersTable bool
		if err := connection.QueryRow(ctx, `SELECT to_regclass('public.users') IS NOT NULL`).Scan(&usersTable); err != nil {
			t.Fatal(err)
		}
		if usersTable {
			t.Fatal("unexpected users table")
		}
	})

	t.Run("restrictive foreign keys and composite request identity", func(t *testing.T) {
		rows, err := connection.Query(ctx, `SELECT c.conrelid::regclass::text, c.confrelid::regclass::text, c.confdeltype, pg_get_constraintdef(c.oid) FROM pg_constraint c WHERE c.contype = 'f' AND c.conrelid IN ('user_capability_grants'::regclass, 'verification_requests'::regclass, 'verification_responses'::regclass)`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var count int
		var composite bool
		for rows.Next() {
			var source, target, action, definition string
			if err := rows.Scan(&source, &target, &action, &definition); err != nil {
				t.Fatal(err)
			}
			count++
			if action != "r" && action != "a" {
				t.Errorf("%s -> %s has nonrestrictive delete action %s", source, target, action)
			}
			if target == "users" {
				t.Error("unexpected users foreign key")
			}
			if source == "verification_responses" && target == "verification_requests" && strings.Contains(definition, "FOREIGN KEY (request_id, incident_id) REFERENCES verification_requests(id, incident_id)") {
				composite = true
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Errorf("foreign key count = %d, want 2", count)
		}
		if !composite {
			t.Error("missing composite response/request foreign key")
		}
	})

	t.Run("unique indexes", func(t *testing.T) {
		var active, idempotency, requestPair, targeting, activeTargeting string
		for _, item := range []struct {
			name string
			into *string
		}{{"user_capability_grants_active_unique_idx", &active}, {"verification_responses_idempotency_key", &idempotency}, {"verification_requests_id_incident_id_key", &requestPair}, {"verification_requests_targeted_idempotency_idx", &targeting}, {"verification_requests_active_targeting_lookup_idx", &activeTargeting}} {
			if err := connection.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, item.name).Scan(item.into); err != nil {
				t.Errorf("index %s: %v", item.name, err)
			}
		}
		if !strings.Contains(active, "UNIQUE") || !strings.Contains(active, "(user_id, capability)") || !strings.Contains(active, "revoked_at IS NULL") {
			t.Errorf("active grant index: %s", active)
		}
		if !strings.Contains(idempotency, "UNIQUE") || !strings.Contains(idempotency, "(request_id, verifier_id, idempotency_key)") {
			t.Errorf("idempotency index: %s", idempotency)
		}
		if !strings.Contains(requestPair, "UNIQUE") || !strings.Contains(requestPair, "(id, incident_id)") {
			t.Errorf("request composite key index: %s", requestPair)
		}
		if !strings.Contains(targeting, "UNIQUE") || !strings.Contains(targeting, "(incident_id, assigned_verifier_id, targeting_policy_version, targeting_policy_fingerprint)") || !strings.Contains(targeting, "assigned_verifier_id IS NOT NULL") || !strings.Contains(targeting, "targeting_policy_version IS NOT NULL") || !strings.Contains(targeting, "targeting_policy_fingerprint IS NOT NULL") {
			t.Errorf("targeting idempotency index: %s", targeting)
		}
		if strings.Contains(activeTargeting, "UNIQUE") || !strings.Contains(activeTargeting, "(incident_id, assigned_verifier_id, expires_at)") || !strings.Contains(activeTargeting, "cancelled_at IS NULL") || !strings.Contains(activeTargeting, "assigned_verifier_id IS NOT NULL") {
			t.Errorf("active targeting lookup index: %s", activeTargeting)
		}
	})

	incidentID := "00000000-0000-0000-0000-000000000101"
	otherIncidentID := "00000000-0000-0000-0000-000000000102"
	userID := "00000000-0000-0000-0000-000000000201"
	operatorID := "00000000-0000-0000-0000-000000000202"
	requestID := "00000000-0000-0000-0000-000000000301"
	if _, err := connection.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state) VALUES ($1, 'UNDECIDED', 'UNDECIDED'), ($2, 'UNDECIDED', 'UNDECIDED')`, incidentID, otherIncidentID); err != nil {
		t.Fatal(err)
	}
	t.Run("verification request lifetime constraints", func(t *testing.T) {
		createdAt := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
		for _, tc := range []struct {
			name        string
			id          string
			expiresAt   time.Time
			cancelledAt any
			wantReject  bool
		}{
			{"expiration equal to creation rejected", "00000000-0000-0000-0000-000000000401", createdAt, nil, true},
			{"expiration before creation rejected", "00000000-0000-0000-0000-000000000402", createdAt.Add(-time.Second), nil, true},
			{"expiration after creation accepted", "00000000-0000-0000-0000-000000000403", createdAt.Add(time.Second), nil, false},
			{"cancellation before creation rejected", "00000000-0000-0000-0000-000000000404", createdAt.Add(time.Hour), createdAt.Add(-time.Second), true},
			{"cancellation equal to creation accepted", "00000000-0000-0000-0000-000000000405", createdAt.Add(time.Hour), createdAt, false},
			{"null cancellation accepted", "00000000-0000-0000-0000-000000000406", createdAt.Add(time.Hour), nil, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := connection.Exec(ctx, `INSERT INTO verification_requests (id, incident_id, created_at, expires_at, cancelled_at) VALUES ($1, $2, $3, $4, $5)`, tc.id, incidentID, createdAt, tc.expiresAt, tc.cancelledAt)
				if tc.wantReject {
					if err == nil {
						t.Fatal("invalid request lifetime accepted")
					}
					return
				}
				if err != nil {
					t.Fatalf("valid request lifetime rejected: %v", err)
				}
			})
		}
	})

	t.Run("grant and revocation provenance", func(t *testing.T) {
		bad := []struct {
			name, sql string
			args      []any
		}{
			{"system grant with actor", `INSERT INTO user_capability_grants (user_id, capability, granted_by, grant_provenance) VALUES ($1, 'trusted_verifier', $2, 'operational_system')`, []any{userID, operatorID}},
			{"principal grant without actor", `INSERT INTO user_capability_grants (user_id, capability, grant_provenance) VALUES ($1, 'trusted_verifier', 'principal')`, []any{userID}},
			{"unknown grant provenance", `INSERT INTO user_capability_grants (user_id, capability, grant_provenance) VALUES ($1, 'trusted_verifier', 'unknown')`, []any{userID}},
		}
		for _, tc := range bad {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := connection.Exec(ctx, tc.sql, tc.args...); err == nil {
					t.Fatal("inconsistent grant accepted")
				}
			})
		}
		var grantID string
		if err := connection.QueryRow(ctx, `INSERT INTO user_capability_grants (user_id, capability, granted_by, grant_provenance) VALUES ($1, 'trusted_verifier', NULL, 'operational_system') RETURNING id`, userID).Scan(&grantID); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Exec(ctx, `INSERT INTO user_capability_grants (user_id, capability, granted_by, grant_provenance) VALUES ($1, 'trusted_verifier', NULL, 'operational_system')`, userID); err == nil {
			t.Error("second active grant accepted")
		}
		for _, tc := range []struct {
			name, sql string
			args      []any
		}{
			{"revocation actor without timestamp", `UPDATE user_capability_grants SET revoked_by = $2, revocation_provenance = 'principal' WHERE id = $1`, []any{grantID, operatorID}},
			{"system revocation with actor", `UPDATE user_capability_grants SET revoked_at = now(), revoked_by = $2, revocation_provenance = 'operational_system' WHERE id = $1`, []any{grantID, operatorID}},
			{"principal revocation without actor", `UPDATE user_capability_grants SET revoked_at = now(), revocation_provenance = 'principal' WHERE id = $1`, []any{grantID}},
			{"revocation missing provenance", `UPDATE user_capability_grants SET revoked_at = now() WHERE id = $1`, []any{grantID}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := connection.Exec(ctx, tc.sql, tc.args...); err == nil {
					t.Fatal("inconsistent revocation accepted")
				}
			})
		}
		if _, err := connection.Exec(ctx, `UPDATE user_capability_grants SET revoked_at = now(), revoked_by = NULL, revocation_provenance = 'operational_system' WHERE id = $1`, grantID); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Exec(ctx, `INSERT INTO user_capability_grants (user_id, capability, granted_by, grant_provenance) VALUES ($1, 'trusted_verifier', $2, 'principal')`, userID, operatorID); err != nil {
			t.Fatalf("regrant after revocation: %v", err)
		}
	})

	t.Run("response dimensions fingerprint and immutable references", func(t *testing.T) {
		if _, err := connection.Exec(ctx, `INSERT INTO verification_requests (id, incident_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, requestID, incidentID); err != nil {
			t.Fatal(err)
		}
		base := `INSERT INTO verification_responses (request_id, incident_id, verifier_id, conclusion, observation, payload_fingerprint_version, payload_fingerprint, idempotency_key) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
		digest := make([]byte, 32)
		for _, tc := range []struct {
			name                    string
			incident                string
			conclusion, observation any
			version                 int
			digest                  []byte
			key                     string
		}{
			{"no dimensions", incidentID, nil, nil, 1, digest, "no-dimensions"},
			{"bad conclusion", incidentID, "YES", nil, 1, digest, "bad-conclusion"},
			{"bad observation", incidentID, nil, "LOOKED", 1, digest, "bad-observation"},
			{"bad version", incidentID, "CONFIRM", nil, 2, digest, "bad-version"},
			{"bad digest", incidentID, "CONFIRM", nil, 1, []byte{1}, "bad-digest"},
			{"wrong incident", otherIncidentID, "CONFIRM", nil, 1, digest, "wrong-incident"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := connection.Exec(ctx, base, requestID, tc.incident, userID, tc.conclusion, tc.observation, tc.version, tc.digest, tc.key); err == nil {
					t.Fatal("invalid response accepted")
				}
			})
		}
		if _, err := connection.Exec(ctx, base, requestID, incidentID, userID, "CONFIRM", "SAW", 1, digest, "key-1"); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.Exec(ctx, base, requestID, incidentID, userID, "DISPUTE", nil, 1, digest, "key-1"); err == nil {
			t.Error("duplicate idempotency key accepted")
		}
		if _, err := connection.Exec(ctx, `DELETE FROM verification_requests WHERE id = $1`, requestID); err == nil {
			t.Error("request with accepted response deleted")
		}
		if _, err := connection.Exec(ctx, `DELETE FROM incidents WHERE id = $1`, incidentID); err == nil {
			t.Error("incident with accepted response deleted")
		}
	})

	runGooseTo(t, ctx, testURL.String(), "202609270001")
	var targetingIndex bool
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.verification_requests_targeted_idempotency_idx') IS NOT NULL`).Scan(&targetingIndex); err != nil {
		t.Fatal(err)
	}
	if targetingIndex {
		t.Fatal("targeting idempotency index remains after migration down")
	}
	var activeTargetingIndex bool
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.verification_requests_active_targeting_lookup_idx') IS NOT NULL`).Scan(&activeTargetingIndex); err != nil {
		t.Fatal(err)
	}
	if activeTargetingIndex {
		t.Fatal("active targeting lookup index remains after migration down")
	}
	var targetingColumn bool
	if err := connection.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='verification_requests' AND column_name='targeting_policy_version')`).Scan(&targetingColumn); err != nil {
		t.Fatal(err)
	}
	if targetingColumn {
		t.Fatal("targeting policy column remains after migration down")
	}
	runGoose(t, ctx, testURL.String(), "up")
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.verification_requests_targeted_idempotency_idx') IS NOT NULL`).Scan(&targetingIndex); err != nil {
		t.Fatal(err)
	}
	if !targetingIndex {
		t.Fatal("targeting idempotency index missing after migration reapply")
	}
	if err := connection.QueryRow(ctx, `SELECT to_regclass('public.verification_requests_active_targeting_lookup_idx') IS NOT NULL`).Scan(&activeTargetingIndex); err != nil {
		t.Fatal(err)
	}
	if !activeTargetingIndex {
		t.Fatal("active targeting lookup index missing after migration reapply")
	}

	if err := connection.Close(ctx); err != nil {
		t.Fatal(err)
	}
	runGoose(t, ctx, testURL.String(), "down")
	connection, err = pgx.Connect(ctx, testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"verification_responses", "verification_requests", "user_capability_grants"} {
		assertTableMissing(t, ctx, connection, "public", table)
	}
	assertTableExists(t, ctx, connection, "public", "incidents")
}
