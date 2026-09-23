# 0010 — AI extraction result destination blocker

**Status:** Blocked pending owner direction
**Scope:** COD-191 structured AI text extraction

## Context

COD-191 validates a structured extraction from a `report.created.v1` event, but the repository does not yet approve where that result should be durably attached, audited, or published. PostgreSQL is the system of record, while Redis Streams is transport; silently logging a result and acknowledging the stream message would discard application state.

## Current boundary

The worker uses `LoggingObserver` only as an explicit boundary. It logs contract metadata without report text or evidence quotes, then returns `ErrDurableExtractionDestinationUnresolved`. The processor therefore does not acknowledge the Redis message, preserving retry visibility while the destination is unresolved.

## Owner decision required

The owner must choose and approve the durable result behavior before this blocker can be removed. That decision must define the destination, idempotency key, transaction/ack ordering, and any downstream contract. This issue does not invent a table, event, or service to resolve it.

## Consequence

The default worker intentionally leaves successfully validated extraction messages pending. Tests may use an in-memory observer to verify the processor boundary, but production ACK/persistence behavior remains blocked until the owner decision is recorded in a follow-up ADR or issue.
