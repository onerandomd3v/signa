# Trusted verifier request targeting

COD-220 creates passive verification requests for trusted verifiers. It only
selects people who may already know or safely observe something relevant; it
never asks anyone to travel, investigate, approach an incident, or gather
recordings. The COD-219 safety prompt remains authoritative.

## Durable trigger and eligibility

The worker handles `incident.created.v1` and `incident.report_attached.v1` from
the durable incident stream. It first completes the existing lifecycle
processor, then targets requests in a separate PostgreSQL transaction. The
consumer acknowledges the event only after both steps succeed. Redelivery is
safe; targeting failures leave the event pending and do not block report
acceptance.

Targeting locks the incident and proceeds only while it is `OPEN` or
`RESOLVING`, has not expired, and remains eligible for the existing
`PublicIncident` projection. It never falls back to raw reports or makes a
private incident visible. PostGIS is authoritative: a fresh current
`user_locations` snapshot must be within the configured distance of the
incident's usable affected geometry, or its center point when no usable area is
available. The candidate query returns no coordinates and orders by distance,
then opaque user UUID, before applying the configured maximum.

A candidate also needs an active, non-revoked `trusted_verifier` grant. The
grant and current location rows are locked while requests are committed, so
operational revocation or location deletion is serialized against targeting.
Missing/future/stale locations, ordinary or revoked users, out-of-radius
locations, ineligible incidents, existing active assignments, and the
candidate cap suppress request creation. These are internal selection rules;
the API does not expose candidate identities or suppression details.

## Explicit policy configuration

Targeting has no built-in pilot thresholds. Configure all four values together
to enable it:

| Environment variable | Meaning |
| --- | --- |
| `SIGNA_VERIFICATION_TARGETING_RADIUS_METERS` | Maximum PostGIS targeting radius |
| `SIGNA_VERIFICATION_TARGETING_LOCATION_MAX_AGE` | Maximum age of a current location snapshot |
| `SIGNA_VERIFICATION_TARGETING_MAX_CANDIDATES` | Maximum targeted verifiers per incident and policy context (1–1000) |
| `SIGNA_VERIFICATION_TARGETING_REQUEST_LIFETIME` | Maximum request lifetime |

If all four values are unset, only verifier targeting is disabled and the
worker continues its other work. Partial or invalid configuration also fails
closed and disables targeting with a low-detail operator warning. No radius,
age, candidate cap, or lifetime is inferred from alert policy. The policy is
versioned as `signa.verification-targeting.v1`; request lifetime is shortened
to the incident expiry when that is sooner.

The environment examples intentionally leave the values blank. OpenShip passes
explicit deployment values through when the pilot policy is approved and
provisioned; no product numbers are silently selected here.

## Idempotency, bounds, and audit

Targeting is serialized by the incident row lock and protected by the
`verification_requests_targeted_idempotency_idx` unique index over incident,
verifier, policy version, and a deterministic fingerprint of the policy
configuration. A verifier with an existing active request is suppressed even
if the deployment policy changes. The durable history prevents replay of the
same policy context from creating a second request. Candidate selection is
ordered by PostGIS distance and user UUID and bounded by the configured cap.

Verifier request list/read/submit continue to use COD-219 authorization and the
privacy-safe `PublicIncident` projection. Listing is server-bounded to 100
requests in `created_at DESC, id DESC` order. UUID identities remain opaque;
this feature adds no users table, grant-management endpoint, admin UI, push UX,
or confidence/lifecycle mutation. Existing restrictive foreign keys preserve
accepted response history, and UUID-based grant records have no users-table
cascade path that can erase grant history.

Exact user coordinates and verifier identities remain internal to the database
selection and authorization paths. They are not returned in public projections,
events, logs, or errors. No location history is added and existing
COD-231 retention/deletion semantics continue to apply.
