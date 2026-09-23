# COD-191 Structured AI Text Extraction v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a retryable Redis Streams AI extraction consumer that validates OpenAI structured output against the v0 contract without inventing a persistence destination.

**Architecture:** Add a focused `internal/ai/extraction` package with narrow provider, report-reader, validator, observer, and Redis interfaces. Wire it beside the existing outbox publisher in `cmd/worker`, acknowledge only after validated observation, and leave successful-result persistence explicitly outside COD-191.

**Tech Stack:** Go 1.25+, `go-redis/v9`, `pgx/v5`, OpenAI HTTPS API, JSON Schema Draft 2020-12 validator, Redis Streams consumer groups, Go tests.

**Spec:** `docs/superpowers/specs/2026-09-24-cod-191-structured-ai-text-extraction-v1-design.md`

## Global Constraints

- AI interprets evidence; deterministic application rules own truth, confidence, final severity policy, priority, and alert eligibility.
- PostgreSQL is authoritative for report text; Redis Streams transports retryable events only.
- Preserve v0 identified/ambiguous/unknown semantics and literal report wording.
- Do not add an extraction table, migration, downstream event, or service boundary.
- Never commit API keys, provider secrets, `.env`, or `.env.local`.
- Do not modify unrelated README formatting or npm audit findings.

## Review Focus

- A provider or validator failure must leave the Redis message pending; `internal/ai/extraction/processor_test.go` pins no-ack retry behavior.
- An ambiguous or unknown fixture must not be promoted to an identified value; `internal/ai/extraction/contract_test.go` pins field-state semantics.
- The event payload must be the only source of `report_id`, while raw text comes from PostgreSQL; `internal/ai/extraction/event_test.go` and `processor_test.go` pin this boundary.
- Successful extraction must not imply durable persistence; `cmd/worker` wiring and observer tests pin the explicit result boundary.
- OpenAI response shape and configuration errors must be deterministic without network access; `internal/ai/extraction/openai_test.go` pins HTTP behavior.

---

### Task 1: Create extraction contract types and real schema validation

**Files:**
- Create: `internal/ai/extraction/contract.go`
- Create: `internal/ai/extraction/validator.go`
- Create: `internal/ai/extraction/contract_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

**Interfaces:**
- Produces `type Extraction struct`, field-state types, `NewValidator(schema []byte)`, and `Validate([]byte) (Extraction, error)`.

- [ ] Write failing tests that load every JSON fixture, validate it, and assert representative English, Pidgin/mixed, hearsay, vague/unknown, and ambiguous states; add invalid identified/ambiguous/unknown cases.
- [ ] Run `go test ./internal/ai/extraction -run 'TestFixture|TestFieldState' -v`; confirm failure because the package and validator do not exist.
- [ ] Add typed v0 structures with JSON tags and a real Draft 2020-12 validator dependency; keep provider JSON as the source for validation and typed decoding.
- [ ] Run the focused tests and confirm all valid fixtures pass while invalid state combinations fail.
- [ ] Run `gofmt` on new Go files and `go test ./internal/ai/extraction`.

### Task 2: Add provider interface and OpenAI structured-output adapter

**Files:**
- Create: `internal/ai/extraction/provider.go`
- Create: `internal/ai/extraction/openai.go`
- Create: `internal/ai/extraction/openai_test.go`

**Interfaces:**
- Consumes `Extractor` from Task 1.
- Produces `Provider` with `Extract(context.Context, string) ([]byte, error)` and `OpenAIProvider` configured by API key, model, base URL, and HTTP client.

- [ ] Write failing HTTP-server tests for request authorization, model, structured JSON response format, prompt containing raw text, successful content extraction, HTTP errors, malformed response envelopes, and missing key/model.
- [ ] Run `go test ./internal/ai/extraction -run TestOpenAI -v`; confirm expected failures.
- [ ] Implement the minimal `net/http` adapter using the separate OpenAI-compatible generation schema and no SDK dependency; validate returned output against the unchanged canonical schema; never log or serialize the API key into test output.
- [ ] Run provider tests and confirm all request/response/error cases pass without network access.
- [ ] Run `go vet ./internal/ai/extraction`.

### Task 3: Add versioned event parsing and report-loading processor

**Files:**
- Create: `internal/ai/extraction/event.go`
- Create: `internal/ai/extraction/processor.go`
- Create: `internal/ai/extraction/event_test.go`
- Create: `internal/ai/extraction/processor_test.go`

**Interfaces:**
- Consumes `report.created.v1` stream fields and the Task 2 provider.
- Produces `ParseReportCreated(fields map[string]any) (ReportCreatedEvent, error)`, `Processor.Process(context.Context, StreamMessage) error`, and narrow `ReportReader`, `Provider`, `Validator`, `Observer`, and `StreamClient` interfaces.

- [ ] Write failing parser tests for valid payloads, wrong event names, missing fields, malformed JSON, and invalid UUID report IDs.
- [ ] Run the parser tests and confirm they fail before implementation.
- [ ] Implement strict event parsing using payload `report_id`; do not use aggregate IDs as a substitute or load any location metadata.
- [ ] Write failing processor tests for successful load/extract/validate/observe flow and failures at each stage; assert `XACK` is not called on failure.
- [ ] Implement PostgreSQL raw-text loading, provider invocation, validation, explicit observer invocation, and final acknowledgement ordering.
- [ ] Run focused processor tests and confirm success acknowledges exactly once while every failure remains retryable.

### Task 4: Implement Redis consumer-group loop and worker wiring

**Files:**
- Modify: `internal/ai/extraction/processor.go`
- Create: `internal/ai/extraction/consumer.go`
- Create: `internal/ai/extraction/consumer_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/worker/main.go`
- Modify: `.env.example`

**Interfaces:**
- Consumes `Processor.Process` and existing `outbox.ReportEventsStream`/`outbox.ReportCreatedV1` constants.
- Produces configured consumer-group polling with pending-message retry and new-message processing.

- [ ] Write failing consumer tests for group creation, pending retry, new-message read, successful ack, and failure without ack; add config parsing/default/error tests.
- [ ] Run focused consumer/config tests and confirm expected failures.
- [ ] Implement consumer-group initialization at stream offset `0`, pending-first reads, bounded blocking, cancellation, and logging; ensure no new stream or event type is introduced.
- [ ] Add safe `.env.example` placeholders for `SIGNA_OPENAI_API_KEY`, `SIGNA_OPENAI_MODEL`, `SIGNA_OPENAI_BASE_URL`, `SIGNA_AI_SCHEMA_PATH`, `SIGNA_AI_CONSUMER_GROUP`, `SIGNA_AI_CONSUMER_NAME`, and `SIGNA_AI_POLL_INTERVAL`.
- [ ] Wire the consumer beside the existing outbox publisher using separate canonical/generation schema files; the default observer must surface the unresolved durable-destination architecture blocker without ACKing; do not invent persistence or publish a new result event.
- [ ] Run focused consumer/config tests and `go test ./...`.

### Task 5: Add live integration coverage and verify the branch

**Files:**
- Create: `internal/ai/extraction/consumer_integration_test.go`
- Modify: `README.md` only if the existing setup documentation needs the new safe configuration names; otherwise leave it unchanged.

- [ ] Write an integration test using local Redis that verifies a failed provider leaves a pending message and a subsequent successful provider acknowledges it; use a fake report reader and provider so no OpenAI secret is needed.
- [ ] Run `go test -tags=integration ./internal/ai/extraction -v` against the local Compose services and record any machine policy limitation.
- [ ] Run `go test ./...`, `go vet ./...`, `git diff --check`, and relevant integration tests.
- [ ] Inspect `git diff`, confirm no secrets or unrelated changes, and confirm the README formatting issue and npm audit findings remain untouched.
- [ ] Commit with `COD-191: implement structured AI text extraction v1`.
- [ ] Push `onerandomd3v/cod-191-rep-06-implement-structured-ai-text-extraction-v1` and open the requested PR into `dev`.
