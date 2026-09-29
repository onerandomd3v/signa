package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestVerificationTargetingMigrationUsesConcurrentIdempotentIndexDDL(t *testing.T) {
	contents, err := os.ReadFile("202609290001_add_verification_targeting_idempotency.sql")
	if err != nil {
		t.Fatal(err)
	}
	upDown, _, ok := strings.Cut(string(contents), "-- +goose Down")
	if !ok {
		t.Fatal("migration has no Goose Down section")
	}
	if !strings.HasPrefix(upDown, "-- +goose NO TRANSACTION\n-- +goose Up") {
		t.Fatal("migration must be a Goose no-transaction migration")
	}
	if !strings.Contains(upDown, "CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS verification_requests_targeted_idempotency_idx") || !strings.Contains(upDown, "targeting_policy_version IS NOT NULL") || !strings.Contains(upDown, "targeting_policy_fingerprint IS NOT NULL") {
		t.Fatal("Up must create the targeting idempotency index concurrently and idempotently")
	}
	activeIndexCreate := "CREATE INDEX CONCURRENTLY IF NOT EXISTS verification_requests_active_targeting_lookup_idx"
	activeIndexDrop := "DROP INDEX CONCURRENTLY IF EXISTS verification_requests_active_targeting_lookup_idx"
	if !strings.Contains(upDown, activeIndexCreate) || !strings.Contains(upDown, "ON verification_requests (incident_id, assigned_verifier_id, expires_at)") || !strings.Contains(upDown, "WHERE cancelled_at IS NULL\n      AND assigned_verifier_id IS NOT NULL") {
		t.Fatal("Up must create the active-targeting lookup index with the incident/verifier/expiry predicates")
	}
	if strings.Index(upDown, activeIndexDrop) > strings.Index(upDown, activeIndexCreate) {
		t.Fatal("Up must remove an interrupted concurrent index before retrying its creation")
	}
	down := string(contents[strings.Index(string(contents), "-- +goose Down"):])
	for _, index := range []struct {
		name       string
		definition string
		predicate  string
	}{
		{
			name:       "verification_requests_assigned_list_idx",
			definition: "ON verification_requests (assigned_verifier_id, expires_at, created_at DESC, id DESC)",
			predicate:  "WHERE cancelled_at IS NULL\n      AND assigned_verifier_id IS NOT NULL",
		},
		{
			name:       "verification_requests_unassigned_list_idx",
			definition: "ON verification_requests (expires_at, created_at DESC, id DESC)",
			predicate:  "WHERE cancelled_at IS NULL\n      AND assigned_verifier_id IS NULL",
		},
	} {
		create := "CREATE INDEX CONCURRENTLY IF NOT EXISTS " + index.name
		drop := "DROP INDEX CONCURRENTLY IF EXISTS " + index.name
		if !strings.Contains(upDown, create) || !strings.Contains(upDown, index.definition) || !strings.Contains(upDown, "INCLUDE (incident_id)") || !strings.Contains(upDown, index.predicate) {
			t.Errorf("Up is missing the expected concurrent definition for %s", index.name)
		}
		if !strings.Contains(upDown, drop) || strings.Index(upDown, drop) > strings.Index(upDown, create) {
			t.Errorf("Up must drop %s before retrying its concurrent creation", index.name)
		}
		if !strings.Contains(down, drop) {
			t.Errorf("Down must drop %s concurrently", index.name)
		}
	}
	if !strings.Contains(down, "DROP INDEX CONCURRENTLY IF EXISTS verification_requests_targeted_idempotency_idx") {
		t.Fatal("Down must drop the targeting idempotency index concurrently")
	}
	if !strings.Contains(down, activeIndexDrop) {
		t.Fatal("Down must drop the active-targeting lookup index concurrently")
	}
}
