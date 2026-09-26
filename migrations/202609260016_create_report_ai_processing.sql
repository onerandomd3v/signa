-- +goose Up
CREATE TABLE report_ai_processing (
    report_id UUID PRIMARY KEY REFERENCES reports(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_failed_at TIMESTAMPTZ,
    failure_kind TEXT,
    attempts INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT report_ai_processing_state_check CHECK (state IN ('RUNNING', 'SUCCEEDED', 'FAILED_RETRYABLE', 'FAILED_TERMINAL')),
    CONSTRAINT report_ai_processing_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT report_ai_processing_failure_kind_check CHECK (failure_kind IS NULL OR failure_kind IN ('transient', 'permanent'))
);

-- +goose Down
DROP TABLE IF EXISTS report_ai_processing;
