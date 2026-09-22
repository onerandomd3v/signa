# ADR-0005: Transactional outbox for durable event publication

- Status: Accepted
- Date: 2026-09-22

## Context

Publishing an event separately from a database write can lose work when one operation succeeds and the other fails. Signa must acknowledge accepted reports and other critical transitions without silently losing their downstream processing.

## Decision

When a durable state change requires asynchronous processing, Signa writes the domain change and its outbox event in the same PostgreSQL transaction. An outbox publisher later publishes pending records to Redis Streams and marks each record published only after successful publication.

## Consequences

- The database transaction is the consistency boundary for state and event intent.
- Publication is retryable and may produce duplicate deliveries; consumers must be idempotent.
- Outbox records require operational visibility for pending, failed, and published states.
- Realtime or worker processing must not replace the durable outbox record with fire-and-forget publication.
