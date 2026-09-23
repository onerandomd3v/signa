-- +goose Up
ALTER TABLE reports
    ALTER COLUMN reporter_id DROP NOT NULL,
    ALTER COLUMN source_type DROP NOT NULL,
    ALTER COLUMN eyewitness_claim DROP NOT NULL,
    ALTER COLUMN language DROP NOT NULL,
    ALTER COLUMN evidence_state DROP NOT NULL,
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN request_fingerprint TEXT;

CREATE UNIQUE INDEX reports_idempotency_key_unique_idx
    ON reports (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS reports_idempotency_key_unique_idx;

ALTER TABLE reports
    DROP COLUMN IF EXISTS idempotency_key,
    DROP COLUMN IF EXISTS request_fingerprint;

-- The nullable state is retained on rollback because accepted reports may
-- legitimately contain unknown post-processing fields.
