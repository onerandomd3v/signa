-- +goose Up
CREATE TABLE deliveries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL,
    priority TEXT NOT NULL,
    channel TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'PENDING',
    idempotency_key TEXT NOT NULL UNIQUE,
    payload JSONB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    active_attempt_no INTEGER,
    last_attempt_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    last_error TEXT,
    provider_response TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT deliveries_state_valid CHECK (state IN ('PENDING', 'IN_FLIGHT', 'SUCCEEDED', 'QUARANTINED', 'SKIPPED')),
    CONSTRAINT deliveries_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT deliveries_active_attempt_valid CHECK (active_attempt_no IS NULL OR (active_attempt_no > 0 AND active_attempt_no <= attempts))
);

CREATE INDEX deliveries_retry_idx ON deliveries (next_attempt_at, updated_at) WHERE state = 'PENDING';
CREATE INDEX deliveries_alert_idx ON deliveries (alert_id, state);

CREATE TABLE delivery_attempts (
    id BIGSERIAL PRIMARY KEY,
    delivery_id UUID NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
    attempt_no INTEGER NOT NULL,
    operation_key TEXT NOT NULL,
    state TEXT NOT NULL,
    failure_kind TEXT,
    provider_response TEXT,
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT delivery_attempts_state_valid CHECK (state IN ('STARTED', 'SUCCEEDED', 'FAILED', 'QUARANTINED', 'SKIPPED', 'STALE')),
    CONSTRAINT delivery_attempts_unique_attempt UNIQUE (delivery_id, attempt_no)
);

CREATE TABLE delivery_quarantine (
    delivery_id UUID PRIMARY KEY REFERENCES deliveries(id) ON DELETE CASCADE,
    reason TEXT NOT NULL,
    quarantined_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS delivery_quarantine;
DROP TABLE IF EXISTS delivery_attempts;
DROP TABLE IF EXISTS deliveries;
