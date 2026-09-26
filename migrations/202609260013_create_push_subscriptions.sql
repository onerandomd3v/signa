-- +goose Up
CREATE TABLE push_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    endpoint TEXT NOT NULL,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT push_subscriptions_endpoint_nonempty CHECK (length(endpoint) > 0),
    CONSTRAINT push_subscriptions_p256dh_nonempty CHECK (length(p256dh) > 0),
    CONSTRAINT push_subscriptions_auth_nonempty CHECK (length(auth) > 0)
);

CREATE UNIQUE INDEX push_subscriptions_endpoint_unique_idx
    ON push_subscriptions (endpoint);

CREATE INDEX push_subscriptions_user_idx
    ON push_subscriptions (user_id, updated_at DESC);

-- +goose Down
DROP INDEX IF EXISTS push_subscriptions_user_idx;
DROP INDEX IF EXISTS push_subscriptions_endpoint_unique_idx;
DROP TABLE IF EXISTS push_subscriptions;
