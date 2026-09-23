-- +goose Up
CREATE INDEX incidents_center_point_gist_idx
    ON incidents USING GIST (center_point);

CREATE INDEX incidents_candidate_lookup_idx
    ON incidents (event_type, (COALESCE(last_signal_at, started_at, created_at)))
    WHERE resolved_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS incidents_candidate_lookup_idx;
DROP INDEX IF EXISTS incidents_center_point_gist_idx;
