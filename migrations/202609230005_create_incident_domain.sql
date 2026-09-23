-- +goose Up
CREATE TABLE incidents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT,
    status TEXT NOT NULL,
    confidence_state TEXT NOT NULL,
    severity TEXT,
    center_point GEOGRAPHY(Point, 4326),
    affected_geometry GEOGRAPHY(Geometry, 4326),
    started_at TIMESTAMPTZ,
    last_signal_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX incidents_status_idx ON incidents (status);
CREATE INDEX incidents_confidence_state_idx ON incidents (confidence_state);

ALTER TABLE reports
    ADD CONSTRAINT reports_incident_id_fkey
    FOREIGN KEY (incident_id) REFERENCES incidents(id) ON DELETE SET NULL;

CREATE TABLE incident_reports (
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    report_id UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    similarity DOUBLE PRECISION,
    independence_weight DOUBLE PRECISION,
    contradiction_state TEXT,
    attached_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (incident_id, report_id)
);

CREATE INDEX incident_reports_report_id_idx ON incident_reports (report_id);

CREATE TABLE incident_state_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    changed_field TEXT NOT NULL,
    previous_value TEXT,
    new_value TEXT NOT NULL,
    reason TEXT,
    actor_type TEXT,
    actor_id TEXT,
    rule_version TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    transitioned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX incident_state_history_incident_order_idx
    ON incident_state_history (incident_id, transitioned_at, id);

-- +goose Down
DROP TABLE IF EXISTS incident_state_history;
DROP TABLE IF EXISTS incident_reports;

ALTER TABLE reports
    DROP CONSTRAINT IF EXISTS reports_incident_id_fkey;

DROP TABLE IF EXISTS incidents;
