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
	down := string(contents[strings.Index(string(contents), "-- +goose Down"):])
	if !strings.Contains(down, "DROP INDEX CONCURRENTLY IF EXISTS verification_requests_targeted_idempotency_idx") {
		t.Fatal("Down must drop the targeting idempotency index concurrently")
	}
}
