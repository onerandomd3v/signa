-- +goose Up
CREATE INDEX reports_exact_location_retention_idx
    ON reports (created_at, id)
    WHERE claimed_location IS NOT NULL OR device_location IS NOT NULL;

CREATE INDEX user_locations_retention_idx
    ON user_locations (observed_at, user_id);

CREATE INDEX report_media_retention_idx
    ON report_media (created_at, id);

CREATE INDEX outbox_events_retention_idx
    ON outbox_events (created_at, id)
    WHERE published_at IS NOT NULL;

CREATE INDEX incident_state_history_retention_idx
    ON incident_state_history (transitioned_at, id);

CREATE INDEX delivery_attempts_retention_idx
    ON delivery_attempts (completed_at, id)
    WHERE completed_at IS NOT NULL;

CREATE INDEX delivery_quarantine_retention_idx
    ON delivery_quarantine (quarantined_at, delivery_id);

CREATE INDEX auth_sessions_retention_idx
    ON auth_sessions (
        (CASE WHEN revoked_at IS NOT NULL THEN revoked_at ELSE expires_at END),
        id
    );

-- +goose Down
DROP INDEX IF EXISTS auth_sessions_retention_idx;
DROP INDEX IF EXISTS delivery_quarantine_retention_idx;
DROP INDEX IF EXISTS delivery_attempts_retention_idx;
DROP INDEX IF EXISTS incident_state_history_retention_idx;
DROP INDEX IF EXISTS outbox_events_retention_idx;
DROP INDEX IF EXISTS report_media_retention_idx;
DROP INDEX IF EXISTS user_locations_retention_idx;
DROP INDEX IF EXISTS reports_exact_location_retention_idx;
