# COD-218 RT-08 Report-to-Alert Latency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reconstruct and expose a privacy-safe report-to-alert latency timeline, with bounded operator diagnostics and a no-op-safe OpenTelemetry boundary.

**Architecture:** Use existing PostgreSQL timestamps and relationships for report, outbox, extraction, incident, alert, and delivery stages. Add one small durable AI-processing state table because provider/retryable/terminal failures and AI start/finish times cannot be recovered from current durable data. Keep priority explicitly unavailable until a trustworthy persisted evaluation timestamp exists. Add a focused `internal/latency` read model and `cmd/report-trace` diagnostic; telemetry uses the OpenTelemetry API with its default no-op provider and stable stage names.

**Tech Stack:** Go 1.25, PostgreSQL/PostGIS, pgx, Redis Streams, OpenTelemetry API, goose migrations, JSON CLI output.

**Spec:** Pasted COD-218 issue text supplied by the user; architecture source `docs/architecture.md`, section 28.

## Global Constraints

- Do not invent numeric MVP latency thresholds.
- Do not add a public/browser tracing API.
- Do not expose report text, reporter identity, exact coordinates, route geometry, push endpoints, provider responses, or secrets.
- Preserve existing transaction boundaries, delivery idempotency, retry semantics, SSE authorization, alert authorization, and safe `alert.created.v1` payloads.
- All durations are deterministic, non-negative, and null/unavailable when timestamps do not exist.
- Recent-sample diagnostics are bounded; no unbounded scans.

## Review Focus

- Out-of-order or malformed timestamps must clamp measured durations to zero or remain unavailable, never become negative.
- An unpublished outbox event and a pending delivery must be distinguishable from a terminal delivery quarantine.
- AI provider failures must expose only safe failure categories, never raw provider/error text.
- Missing future stages must not appear as zero-duration successes.
- Multiple alerts/deliveries must resolve deterministically without widening the diagnostic query.

---

### Task 1: Pure latency timeline model

**Files:**
- Create: `internal/latency/timeline.go`
- Test: `internal/latency/timeline_test.go`

**Interfaces:**
- Produces `Timeline`, `Stage`, `StageState`, `StageTiming`, `BuildTimeline(Input) Timeline` for the store and CLI.

- [ ] Write failing tests for completed paths, pending/backlog, retryable and terminal failures, unavailable stages, deterministic selection, and non-negative durations.
- [ ] Run the focused tests and confirm failure because the model is absent.
- [ ] Implement the pure model with nullable duration fields and stable JSON field names.
- [ ] Run focused tests and the package suite.
- [ ] Commit the pure model.

### Task 2: Durable AI stage state

**Files:**
- Create: `migrations/202609260015_create_report_ai_processing.sql`
- Modify: `internal/ai/extraction/durable.go`
- Modify: `internal/ai/extraction/processor.go`
- Test: `internal/ai/extraction/durable_test.go`, `internal/ai/extraction/processor_test.go`

**Interfaces:**
- Produces `AIProcessingState` records with `PENDING`, `RUNNING`, `SUCCEEDED`, `FAILED_RETRYABLE`, and `FAILED_TERMINAL` states; no raw error payloads.

- [ ] Add failing tests for start/success, retryable provider failure, terminal/schema failure, and no-op behavior when the optional recorder is absent.
- [ ] Run focused tests and confirm failure.
- [ ] Add the smallest migration and best-effort recorder methods; keep failure recording observational and non-blocking.
- [ ] Hook processor start/failure/success without changing ACK or retry behavior.
- [ ] Run focused tests and migration/unit checks.
- [ ] Commit the AI state implementation.

### Task 3: PostgreSQL timeline reconstruction and bounded queries

**Files:**
- Create: `internal/latency/store.go`
- Test: `internal/latency/store_test.go`
- Create: `internal/latency/store_integration_test.go`
- Modify: `migrations/migrations_integration_test.go`

**Interfaces:**
- `NewStore(*pgxpool.Pool) *Store`, `TraceReport(context.Context, uuid.UUID) (Timeline, error)`, and `Recent(context.Context, int) ([]Timeline, error)`.

- [ ] Add failing fixture tests for completed report→alert→delivery, unpublished outbox, AI failure/pending, incident/priority gaps, pending delivery, retryable/terminal delivery, privacy-safe output, and recent limit enforcement.
- [ ] Run focused tests and confirm failure.
- [ ] Implement bounded SQL using existing indexes and a deterministic first report outbox/alert/delivery path; do not select sensitive columns.
- [ ] Map durable rows into the pure timeline and derive report-to-alert/report-to-delivery only from real timestamps.
- [ ] Run unit tests and PostgreSQL integration tests when the local database is available.
- [ ] Commit the store and migration coverage.

### Task 4: OpenTelemetry boundary and stage instrumentation

**Files:**
- Create: `internal/observability/observability.go`
- Test: `internal/observability/observability_test.go`
- Modify: `go.mod`, `go.sum`
- Modify: `internal/reports/reports.go`
- Modify: `internal/outbox/outbox.go`
- Modify: `internal/ai/extraction/processor.go`
- Modify: `internal/incidents/processor.go`
- Modify: `internal/priority/service.go`
- Modify: `internal/alerts/store.go`
- Modify: `internal/delivery/processor.go`

- [ ] Add failing no-op tests proving spans/metrics can be used with no configured collector and do not alter returned behavior.
- [ ] Run focused tests and confirm failure.
- [ ] Add narrowly scoped OpenTelemetry API instrumentation with stable low-cardinality stage/status attributes and no identifiers.
- [ ] Wrap existing stage boundaries without moving transaction or ACK boundaries.
- [ ] Run affected package tests.
- [ ] Commit telemetry changes.

### Task 5: Operator diagnostic command and documentation

**Files:**
- Create: `cmd/report-trace/main.go`
- Test: `cmd/report-trace/main_test.go`
- Create: `docs/observability/report-to-alert-latency.md`
- Modify: `README.md`

- [ ] Add failing CLI tests for report ID mode, bounded recent mode, stable JSON, privacy exclusions, and clear database/config errors.
- [ ] Run focused tests and confirm failure.
- [ ] Implement `go run ./cmd/report-trace -report-id <uuid>` and `-limit <1..100>` using `SIGNA_DATABASE_URL`/repository config; emit no health score.
- [ ] Document stage definitions, correlation, privacy rules, status classifications, diagnostic examples, OpenTelemetry configuration boundary, and the absence of numeric MVP thresholds.
- [ ] Run CLI and documentation checks.
- [ ] Commit command and docs.

### Task 6: Whole-branch verification and PR preparation

**Files:**
- Modify only files required by review findings.

- [ ] Run `go test ./...`, `go vet ./...`, `golangci-lint v2.13.2 run`, and `git diff --check`.
- [ ] Run relevant PostgreSQL/Docker integration tests if available and classify environment-only/pre-existing failures separately.
- [ ] Re-read the issue and inspect the complete diff for privacy, SLA invention, unrelated changes, and preserved safety boundaries.
- [ ] Rebase/sync onto latest `origin/dev`, rerun required checks, create one PR targeting `dev`, and do not merge.
