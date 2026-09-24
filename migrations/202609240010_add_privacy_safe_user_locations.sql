-- +goose Up
CREATE TABLE user_locations (
    user_id UUID PRIMARY KEY,
    location GEOGRAPHY(Point, 4326) NOT NULL,
    accuracy_meters DOUBLE PRECISION,
    observed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_locations_accuracy_nonnegative_finite
        CHECK (
            accuracy_meters IS NULL
            OR (
                accuracy_meters >= 0
                AND accuracy_meters < 'Infinity'::double precision
            )
        )
);

CREATE INDEX user_locations_location_gist_idx
    ON user_locations USING GIST (location);

CREATE INDEX incidents_affected_geometry_gist_idx
    ON incidents USING GIST (affected_geometry);

-- +goose Down
DROP INDEX IF EXISTS incidents_affected_geometry_gist_idx;
DROP INDEX IF EXISTS user_locations_location_gist_idx;
DROP TABLE IF EXISTS user_locations;
