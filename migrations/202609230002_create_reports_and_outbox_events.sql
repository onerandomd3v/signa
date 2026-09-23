-- +goose Up
CREATE TABLE reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id UUID NOT NULL,
    incident_id UUID,
    raw_text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    source_type TEXT NOT NULL,
    eyewitness_claim TEXT NOT NULL,
    claimed_location GEOGRAPHY(Point, 4326) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    device_location GEOGRAPHY(Point, 4326),
    location_accuracy DOUBLE PRECISION,
    language TEXT NOT NULL,
    evidence_state TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX reports_claimed_location_gist_idx
    ON reports USING GIST (claimed_location);

CREATE INDEX reports_device_location_gist_idx
    ON reports USING GIST (device_location);

CREATE INDEX reports_incident_id_idx
    ON reports (incident_id)
    WHERE incident_id IS NOT NULL;

CREATE TABLE outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id UUID NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    retry_attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    CONSTRAINT outbox_events_retry_attempts_nonnegative CHECK (retry_attempts >= 0)
);

CREATE INDEX outbox_events_unpublished_idx
    ON outbox_events (created_at, id)
    WHERE published_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS reports;
