# COD-191: Structured AI Text Extraction v1

## Goal

Consume `report.created.v1` events, load the authoritative report text from PostgreSQL, produce a versioned v0 structured extraction through an OpenAI provider, validate every successful result against the checked-in Draft 2020-12 schema, and acknowledge Redis messages only after successful processing.

## Scope and constraints

- AI output is evidence interpretation only. It must not set incident truth, final confidence, final severity policy, P1/P2/P3 priority, or alert eligibility.
- The report event payload supplies `report_id`; PostgreSQL remains authoritative for `raw_text`.
- The consumer uses the existing `signa:report-events` Redis Stream and consumer-group architecture.
- Provider and configuration interfaces keep unit tests independent of OpenAI credentials and network access.
- The existing v0 `identified`, `ambiguous`, and `unknown` semantics are preserved exactly, including literal evidence quotes and un-resolved location/time wording.
- No migration, extraction table, downstream event, or service boundary is added. The repository has no approved durable destination for successful extraction output. The default `LoggingObserver` therefore surfaces `ErrDurableExtractionDestinationUnresolved` after logging metadata, and the message remains pending. Owner direction is required before ACK/persistence behavior can be finalized.
- Failures leave the stream message pending and retryable. The default worker does not acknowledge a validated result until an approved durable destination replaces the blocker observer.
- OpenAI credentials are read only from environment configuration and never appear in repository files.

## Design

### Extraction package

`internal/ai/extraction` owns the v0 contract model, schema validator, provider interface, OpenAI HTTP provider, report-text loader interface, event parser, and stream processor. The processor accepts narrow interfaces for PostgreSQL, Redis, provider, validator, and result observation so tests can use fakes.

The OpenAI provider uses the structured JSON response format with the OpenAI-compatible generation schema at `contracts/ai/extraction/v0/openai.schema.json`. The validator is a real JSON Schema Draft 2020-12 implementation initialized from the unchanged canonical `contracts/ai/extraction/v0/schema.json`; it rejects malformed JSON and conditional contract violations before the result can be acknowledged.

### Stream processing

The worker ensures a named consumer group exists at stream offset `0`, so reports already in the stream before first startup are consumed. It first services pending messages for retry visibility, then reads new messages. It accepts only `report.created.v1` messages from `signa:report-events`, parses the payload's `report_id`, loads `raw_text`, extracts and validates the result, invokes the observer, and calls `XACK` last. Parse, database, provider, validation, and ordinary observer errors return without acknowledgement and remain retryable. `ErrDurableExtractionDestinationUnresolved` is different: the consumer records that message ID in a process-local quarantine, leaves it pending without another provider call, and keeps the consumer loop alive for other work.

### Configuration

Add safe placeholders for the OpenAI API key, model/base URL, schema path, extraction consumer group/name, and extraction poll interval. Existing defaults remain valid for local development without an API key; the production worker fails clearly when OpenAI processing is attempted without the required key.

## Test coverage

- Schema validator accepts all supported fixtures and rejects invalid state combinations.
- Fixture assertions cover English, Nigerian Pidgin/mixed language, hearsay, vague/unknown, and ambiguous extraction semantics.
- Event parsing rejects wrong event names, malformed payloads, and missing/invalid report IDs.
- Provider tests cover request shape, structured response parsing, HTTP errors, malformed provider JSON, and missing configuration without network calls.
- Processor tests cover report loading, successful acknowledgement, and no-ack retry behavior for provider, validation, observer, and database failures.
- Redis integration tests exercise a real consumer group and verify failed messages remain pending while successful messages are acknowledged.

## Out of scope

- Persisting extraction results.
- Publishing a new extraction event.
- Geocoding, timestamp normalization, incident creation, confidence/severity policy, alerting, media processing, or frontend changes.
