-- +goose Up
CREATE TABLE web_push_delivery_results (
    delivery_id UUID NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
    subscription_id UUID NOT NULL,
    state TEXT NOT NULL DEFAULT 'PENDING',
    provider_response TEXT,
    error_message TEXT,
    claimed_until TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (delivery_id, subscription_id),
    CONSTRAINT web_push_delivery_results_state_check CHECK (state IN ('PENDING', 'SUCCEEDED', 'EXPIRED', 'PERMANENT', 'RETRYABLE'))
);

CREATE INDEX web_push_delivery_results_delivery_state_idx
    ON web_push_delivery_results (delivery_id, state);

-- +goose Down
DROP INDEX IF EXISTS web_push_delivery_results_delivery_state_idx;
DROP TABLE IF EXISTS web_push_delivery_results;
