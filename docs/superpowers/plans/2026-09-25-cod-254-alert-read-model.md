# COD-254 Authenticated Alert Read Model Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Expose the authoritative authenticated alert snapshot required to reconcile `alert.created.v1` without treating SSE payloads as display state.

**Architecture:** Read one alert through a bounded PostgreSQL `alerts JOIN deliveries` query scoped to the authenticated principal. Mount `GET /alerts/{alert_id}` behind existing CookieAuth middleware, return only the immutable safe snapshot, and keep SSE payloads limited to stable reconciliation identifiers.

**Tech Stack:** Go, Chi, pgx/pgxpool, PostgreSQL, OpenAPI 3.1, Hey API generated TypeScript client.

**Spec:** `C:/Users/HP/.codex/attachments/6ef7e2ae-03ea-4a3c-832d-a012f5842624/goal-objective.md`

## Global Constraints

- Identity comes only from `auth.Principal` resolved from the `signa_session` CookieAuth middleware.
- Authorization uses durable delivery state; arbitrary authenticated users cannot read alert UUIDs.
- Missing or terminally undisclosed delivery visibility returns privacy-safe `404`.
- The response omits provider/subscription/retry state, reporter identity, raw evidence, exact private coordinates, and auth/session secrets.
- Do not change COD-213 frontend behavior beyond generated client output.
- No migration is expected; do not collide with PR #51 or PR #56 migration identifiers.

## Review Focus

- Cross-user UUID access is denied by the delivery predicate.
- `SKIPPED` and `QUARANTINED` deliveries are not visible; pending/in-flight/succeeded remain visible.
- Stale, superseded, resolving, resolved, and expired snapshots are returned unchanged.
- Session-store failure remains JSON `503`, not `401`.
- Generated client output stays aligned with OpenAPI and CookieAuth.

### Task 1: Durable authorized alert read operation

**Files:** `internal/alerts/read.go`, `internal/alerts/read_test.go`, `internal/alerts/read_integration_test.go`.

- [x] Add failing tests for safe fields, identity arguments, missing visibility, cross-user access, terminal delivery state, and immutable lifecycle values.
- [x] Implement `alerts.AlertRead`, `alerts.AlertReader`, `ErrAlertNotVisible`, and `(*alerts.Store).ReadAuthorized(ctx, userID, alertID)` with one parameterized bounded query.
- [x] Run unit tests and integration-tag compilation; record PostgreSQL unavailability for runtime integration execution.

### Task 2: Authenticated HTTP endpoint and SSE reconciliation

**Files:** `internal/api/alerts.go`, `internal/api/alerts_test.go`, `internal/api/server.go`, `internal/realtime/sse_test.go`.

- [x] Add tests for canonical `401`, auth-store `503`, malformed UUID `400`, authorized `200`, privacy-safe `404`, ignored client `user_id`, and safe-field exclusion.
- [x] Mount `GET /alerts/{alert_id}` behind the existing principal middleware and wire the production PostgreSQL alert store through the existing API pool.
- [x] Confirm `alert.created.v1` carries stable `alert_id`/`incident_id` identifiers without becoming a full read model.

### Task 3: OpenAPI and generated client

**Files:** `contracts/openapi.yaml`, generated files under `apps/web/lib/api/generated/`.

- [x] Add `AlertID`, `AlertRead`, CookieAuth-protected `getAlert`, and implemented `400/401/404/503` errors.
- [x] Regenerate the TypeScript client and run the API check.

### Task 4: Validation and delivery

- [x] Run backend tests, vet, linter, integration-tag compile, frontend API/lint/typecheck/test/build checks, and diff checks.
- [ ] Runtime integration execution remains pending PostgreSQL/Redis availability.
- [ ] Fetch latest `origin/dev`, rebase if needed, push the issue branch, and open the PR targeting `dev`; do not merge.
