-- +goose Up
CREATE TABLE report_media (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    object_key TEXT NOT NULL UNIQUE,
    media_type TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX report_media_report_id_idx ON report_media (report_id, created_at, id);

-- +goose Down
DROP TABLE IF EXISTS report_media;
