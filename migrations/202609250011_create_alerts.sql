-- +goose Up
CREATE TABLE alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE RESTRICT,
    alert_type TEXT NOT NULL,
    confidence_snapshot TEXT NOT NULL,
    severity_snapshot TEXT NOT NULL,
    status_snapshot TEXT NOT NULL,
    priority_snapshot TEXT NOT NULL,
    freshness_snapshot TEXT NOT NULL,
    message TEXT NOT NULL,
    eligibility_policy_version TEXT NOT NULL,
    eligibility_reasons JSONB NOT NULL DEFAULT '[]'::jsonb,
    as_of TIMESTAMPTZ NOT NULL,
    supersedes_alert_id UUID REFERENCES alerts(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,
    request_fingerprint TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT alerts_not_self_superseding CHECK (supersedes_alert_id IS NULL OR supersedes_alert_id <> id),
    CONSTRAINT alerts_eligibility_reasons_array CHECK (jsonb_typeof(eligibility_reasons) = 'array')
);

CREATE INDEX alerts_incident_created_idx ON alerts (incident_id, created_at, id);
CREATE INDEX alerts_supersedes_alert_id_idx ON alerts (supersedes_alert_id) WHERE supersedes_alert_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS alerts;
