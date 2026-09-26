-- +goose Up
CREATE TABLE auth_sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT auth_sessions_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT auth_sessions_expiry_valid CHECK (expires_at > created_at)
);

CREATE INDEX auth_sessions_active_lookup_idx
    ON auth_sessions (token_hash, expires_at)
    WHERE revoked_at IS NULL;

CREATE INDEX auth_sessions_user_idx ON auth_sessions (user_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS auth_sessions;
