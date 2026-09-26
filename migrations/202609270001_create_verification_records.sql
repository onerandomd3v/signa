-- +goose Up
CREATE TABLE user_capability_grants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    capability TEXT NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    granted_by UUID,
    grant_provenance TEXT NOT NULL,
    revoked_at TIMESTAMPTZ,
    revoked_by UUID,
    revocation_provenance TEXT,
    CONSTRAINT user_capability_grants_grant_provenance_check CHECK (
        (grant_provenance = 'principal' AND granted_by IS NOT NULL)
        OR (grant_provenance = 'operational_system' AND granted_by IS NULL)
    ),
    CONSTRAINT user_capability_grants_revocation_provenance_check CHECK (
        (revoked_at IS NULL AND revoked_by IS NULL AND revocation_provenance IS NULL)
        OR (revoked_at IS NOT NULL AND revocation_provenance IS NOT NULL AND (
            (revocation_provenance = 'principal' AND revoked_by IS NOT NULL)
            OR (revocation_provenance = 'operational_system' AND revoked_by IS NULL)
        ))
    )
);

CREATE UNIQUE INDEX user_capability_grants_active_unique_idx
    ON user_capability_grants (user_id, capability) WHERE revoked_at IS NULL;

CREATE TABLE verification_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES incidents(id) ON DELETE RESTRICT,
    assigned_verifier_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    cancelled_at TIMESTAMPTZ,
    CONSTRAINT verification_requests_id_incident_id_key UNIQUE (id, incident_id)
);

CREATE INDEX verification_requests_active_order_idx
    ON verification_requests (created_at DESC, id DESC)
    WHERE cancelled_at IS NULL;

CREATE TABLE verification_responses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL,
    incident_id UUID NOT NULL,
    verifier_id UUID NOT NULL,
    conclusion TEXT,
    observation TEXT,
    payload_fingerprint_version SMALLINT NOT NULL CHECK (payload_fingerprint_version = 1),
    payload_fingerprint BYTEA NOT NULL CHECK (octet_length(payload_fingerprint) = 32),
    idempotency_key VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT verification_responses_conclusion_check CHECK (
        conclusion IS NULL OR conclusion IN ('CONFIRM', 'CANNOT_CONFIRM', 'DISPUTE')
    ),
    CONSTRAINT verification_responses_observation_check CHECK (
        observation IS NULL OR observation IN ('SAW', 'HEARD')
    ),
    CONSTRAINT verification_responses_dimension_check CHECK (
        conclusion IS NOT NULL OR observation IS NOT NULL
    ),
    CONSTRAINT verification_responses_request_incident_fkey
        FOREIGN KEY (request_id, incident_id)
        REFERENCES verification_requests(id, incident_id) ON DELETE RESTRICT,
    CONSTRAINT verification_responses_idempotency_key
        UNIQUE (request_id, verifier_id, idempotency_key)
);

-- +goose Down
DROP TABLE verification_responses;
DROP TABLE verification_requests;
DROP TABLE user_capability_grants;
