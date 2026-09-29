-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE verification_requests
    ADD COLUMN IF NOT EXISTS targeting_policy_version TEXT;
ALTER TABLE verification_requests
    ADD COLUMN IF NOT EXISTS targeting_policy_fingerprint BYTEA;

-- If a previous CONCURRENTLY build was interrupted before Goose recorded this
-- migration, remove its possibly-invalid index before retrying the build.
DROP INDEX CONCURRENTLY IF EXISTS verification_requests_targeted_idempotency_idx;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS verification_requests_targeted_idempotency_idx
    ON verification_requests (incident_id, assigned_verifier_id, targeting_policy_version, targeting_policy_fingerprint)
    WHERE assigned_verifier_id IS NOT NULL
      AND targeting_policy_version IS NOT NULL
      AND targeting_policy_fingerprint IS NOT NULL;

-- Keep active-assignment exclusion bounded by incident and verifier while
-- skipping unassigned and cancelled request history.
DROP INDEX CONCURRENTLY IF EXISTS verification_requests_active_targeting_lookup_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS verification_requests_active_targeting_lookup_idx
    ON verification_requests (incident_id, assigned_verifier_id, expires_at)
    WHERE cancelled_at IS NULL
      AND assigned_verifier_id IS NOT NULL;

-- Verifier inbox reads use expiry-first partial indexes so durable expired
-- history is excluded by an index range condition before live rows are sorted.
DROP INDEX CONCURRENTLY IF EXISTS verification_requests_assigned_list_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS verification_requests_assigned_list_idx
    ON verification_requests (assigned_verifier_id, expires_at, created_at DESC, id DESC)
    INCLUDE (incident_id)
    WHERE cancelled_at IS NULL
      AND assigned_verifier_id IS NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS verification_requests_unassigned_list_idx;
CREATE INDEX CONCURRENTLY IF NOT EXISTS verification_requests_unassigned_list_idx
    ON verification_requests (expires_at, created_at DESC, id DESC)
    INCLUDE (incident_id)
    WHERE cancelled_at IS NULL
      AND assigned_verifier_id IS NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS verification_requests_unassigned_list_idx;
DROP INDEX CONCURRENTLY IF EXISTS verification_requests_assigned_list_idx;
DROP INDEX CONCURRENTLY IF EXISTS verification_requests_active_targeting_lookup_idx;
DROP INDEX CONCURRENTLY IF EXISTS verification_requests_targeted_idempotency_idx;
ALTER TABLE verification_requests DROP COLUMN IF EXISTS targeting_policy_fingerprint;
ALTER TABLE verification_requests DROP COLUMN IF EXISTS targeting_policy_version;
