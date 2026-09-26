-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS outbox_events_aggregate_event_idx
    ON outbox_events (aggregate_type, aggregate_id, event_type, created_at, id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS deliveries_alert_created_idx
    ON deliveries (alert_id, created_at, id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS reports_recent_created_idx
    ON reports (created_at DESC, id DESC);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS reports_recent_created_idx;
DROP INDEX CONCURRENTLY IF EXISTS deliveries_alert_created_idx;
DROP INDEX CONCURRENTLY IF EXISTS outbox_events_aggregate_event_idx;
