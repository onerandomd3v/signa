-- +goose Up
CREATE INDEX incidents_lifecycle_due_idx
    ON incidents (status, (COALESCE(last_signal_at, started_at, created_at)));

-- +goose Down
DROP INDEX IF EXISTS incidents_lifecycle_due_idx;
