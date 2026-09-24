-- +goose Up
CREATE TABLE report_ai_extractions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id UUID NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    contract_version TEXT NOT NULL,
    taxonomy_version TEXT NOT NULL,
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT report_ai_extractions_report_contract_unique UNIQUE (report_id, contract_version)
);

CREATE INDEX report_ai_extractions_report_id_idx
    ON report_ai_extractions (report_id, created_at, id);

-- +goose Down
DROP TABLE IF EXISTS report_ai_extractions;
