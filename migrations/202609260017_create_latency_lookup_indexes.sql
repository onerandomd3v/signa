-- +goose NO TRANSACTION
-- +goose Up
-- A failed concurrent build can leave an invalid same-named index behind.
-- Drop it before each build so the migration runner's retry cannot skip it.
DROP INDEX CONCURRENTLY IF EXISTS outbox_events_aggregate_event_idx;
CREATE INDEX CONCURRENTLY outbox_events_aggregate_event_idx
    ON outbox_events (aggregate_type, aggregate_id, event_type, created_at, id);

DROP INDEX CONCURRENTLY IF EXISTS deliveries_alert_created_idx;
CREATE INDEX CONCURRENTLY deliveries_alert_created_idx
    ON deliveries (alert_id, created_at, id);

DROP INDEX CONCURRENTLY IF EXISTS reports_recent_created_idx;
CREATE INDEX CONCURRENTLY reports_recent_created_idx
    ON reports (created_at DESC, id DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS reports_recent_created_idx;
DROP INDEX CONCURRENTLY IF EXISTS deliveries_alert_created_idx;
DROP INDEX CONCURRENTLY IF EXISTS outbox_events_aggregate_event_idx;
