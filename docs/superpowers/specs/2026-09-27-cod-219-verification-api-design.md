# COD-219 — Trusted Verifier Roles and Verification API

Status: conversational design approved; written-spec review pending  
Linear: [COD-219 — VER-01](https://linear.app/codeddevs/issue/COD-219/ver-01-add-trusted-verifier-roles-and-verification-api)  
Target branch: `dev`

## Intent and scope

Establish a server-authoritative trusted-verifier capability and a durable,
privacy-safe API for reading verification requests and recording verifier
responses. Use the canonical `internal/auth.Principal` resolved by the existing
opaque database-backed session cookie. Responses are auditable evidence only;
they do not determine incident truth or mutate confidence. The work is limited
to COD-219's backend foundation.

Out of scope: grant/revoke endpoints, an admin UI or bootstrap API; verifier
targeting/geospatial selection (COD-220); verifier inbox frontend (COD-221);
confidence integration (COD-222); reporter reputation; operator dashboards; new
authentication mechanisms; and changes to alert, delivery, realtime/SSE, or
OpenTelemetry behavior.

## Authorization and grant lifecycle

Add a generalized `user_capability_grants` relation with a row ID, `user_id`,
`capability`, `granted_at`, nullable `granted_by`, explicit grant provenance,
nullable `revoked_at`, nullable `revoked_by`, and explicit revocation
provenance. The only capability consumed by this feature is the exact value
`trusted_verifier`; unknown capability strings are inert. Keep identity UUIDs
without a user-table foreign key because the current auth schema has no durable
users table.

Operational pilot provisioning records `granted_by = NULL` and provenance
`operational_system`; it must not invent an operator principal. If a valid
operator principal is available in a later authorized flow, provenance can
identify that actor with `granted_by`. Enforce consistency between actor and
provenance fields. Revoke by setting `revoked_at` and recording the available
actor/provenance, never by deleting the grant. A later regrant is a new row. A
partial unique index prevents more than one active row for the same user and
capability.

Every verifier-only request resolves the session first and then checks durable
grant state (`revoked_at IS NULL`). No hardcoded IDs, environment allowlists,
client role/capability claims, bearer credentials, or identity asserted in
headers/query parameters are accepted. A revoked grant immediately stops
authorizing reads and writes. Database unavailability fails closed with a safe
service-unavailable response.

## Verification request and response model

Add a `verification_requests` relation containing request ID, incident ID,
optional assigned verifier UUID, creation and expiry times, and cancellation
time. Assignment is a future-compatible field only: COD-219 does not select
targets or expose request creation. Unassigned active requests are available to
trusted verifiers; an assigned request is available only to its assigned user,
and only while that user's trusted-verifier grant remains active. Expired,
cancelled, absent, and differently assigned requests share the same privacy-safe
not-found result.

The verifier read projection combines request metadata with only the existing
generalized public incident projection: public incident ID, event type, status,
confidence state, severity, generalized geometry and freshness timestamps, as
available. Include a fixed prompt instructing users to respond only from what
they already safely know or observed and never approach an incident or unsafe
area to verify it. Do not include reporter ID, raw report text, exact reporter
or device coordinates, private media/storage identifiers, AI/provider data,
delivery internals, session/auth data, or other verifier identities.

Add `verification_responses`, tied to both request and incident, with response
ID, internal verifier UUID, conclusion, observation, idempotency key and
creation timestamp. Conclusion is independently optional and has values
`CONFIRM`, `CANNOT_CONFIRM`, or `DISPUTE`; observation is independently optional
and has values `SAW` or `HEARD`. Require at least one dimension. Responses are
append-only: the API never overwrites or deletes accepted evidence. A unique
boundary on request, verifier and idempotency key handles concurrent retries.
For the same key and same normalized response, return the original response;
for the same key with different content, return `409`. A new key appends a new
auditable response. No response operation changes incident confidence or
lifecycle.

## Module and HTTP API

Implement domain/service/store behavior in a dedicated `internal/verification`
module. `internal/api` is the HTTP adapter and obtains the authenticated
principal from request context; it does not accept identity or capability from
the client. The module depends on PostgreSQL and the safe incident projection,
not raw report queries.

Add these CookieAuth operations to the OpenAPI contract and regenerate the
TypeScript client:

- `GET /v1/verifications/requests` — list active requests visible to the
  principal.
- `GET /v1/verifications/requests/{request_id}` — read one eligible request.
- `POST /v1/verifications/requests/{request_id}/responses` — submit one
  response, requiring `Idempotency-Key`.

Use the repository's existing error envelope and safe fixed messages. Missing
or invalid session is `401`; authenticated non-verifiers are `403`; absent,
expired, cancelled or out-of-audience requests are indistinguishable `404`;
invalid JSON/enums or missing/oversized idempotency key is `400`; key reuse with
different content is `409`; role-store or verification-store unavailability
is a generic `503`. Do not return internal database errors.

## Database migration and operations

Add a new reversible, transactional Goose migration after the current latest
migration to create only the new capability, request and response tables,
constraints and indexes. The migration inserts no grants and does not alter
existing incident, report, session or other busy tables.

Add a concise operations runbook describing deployment/database provisioning
and revocation for the pilot, including how to record `operational_system`
provenance. Do not put real user IDs in source control. No HTTP grant/revoke
API, admin UI or initial-admin bootstrap API is introduced.

## Failure handling, privacy, and compatibility

Use server-side capability checks on every protected operation and fail closed
when grant lookup is unavailable. Apply the same not-found response to missing
and unauthorized targeted resources. Keep reporter identity and exact private
location out of all verifier-facing responses and query projections. Do not
change canonical session principal semantics, incident confidence processing,
alert authorization, delivery behavior, SSE behavior, or telemetry labels.

## Validation and acceptance coverage

- Unit tests: active versus revoked grants; assignment eligibility; request
  expiry/cancellation; conclusion and observation validation; same-key replay
  and mismatched-key conflict; safe errors.
- API tests: unauthenticated `401`; ordinary-user `403`; trusted-verifier
  access; assigned-request cross-user `404`; invalid inputs; safe `503`; and
  absence of reporter/private fields in JSON.
- PostgreSQL integration tests: operational and principal-attributed grants;
  active/revoked authorization; durable request/response reads and writes;
  concurrent same-key retries yielding one response; distinct keys preserving
  append-only history; and proof that response submission does not update
  incident confidence.
- Migration tests: up/down behavior and schema constraints/indexes.
- OpenAPI lint/generation checks ensure generated TypeScript remains in sync.
- Run `go test ./...`, `go vet ./...`, `golangci-lint v2.13.2 run`,
  `npm run api:check`, `npm run lint`, `npm run typecheck`,
  `npm run test -- --pool=threads --maxWorkers=1`, `npm run build`, relevant
  PostgreSQL integration tests when PostgreSQL is available, and
  `git diff --check`. Report environment-only failures accurately.

## Implementation boundary

The implementation starts only after review and approval of this written spec,
followed by an approved implementation plan. It must be built from latest
`origin/dev` on the COD-219 branch, validated, pushed, and submitted as one PR
targeting `dev`. Do not merge.
