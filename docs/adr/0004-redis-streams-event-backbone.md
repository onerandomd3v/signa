# ADR-0004: Redis Streams as the MVP event backbone

- Status: Accepted
- Date: 2026-09-22

## Context

Signa requires asynchronous processing for extraction, incident updates, confidence and priority evaluation, and delivery. The MVP needs low-latency worker coordination with consumer groups and retry visibility without introducing Kafka or a distributed service architecture.

## Decision

Redis Streams is the MVP event backbone. Workers consume named streams through consumer groups, acknowledge messages only after the durable operation succeeds, and preserve pending, retry, and failure visibility.

Redis Streams transports events; it does not own authoritative incident state. PostgreSQL remains the system of record under ADR-0003.

## Consequences

- Event handlers must be idempotent because delivery may occur more than once.
- Stream messages must carry versioned event names and sufficient identifiers to reload durable state.
- Stream retention, pending-entry recovery, and dead-letter handling are operational responsibilities.
- A future change to the event backbone requires an architecture decision.
