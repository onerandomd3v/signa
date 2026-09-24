# ADR-0010: Durable AI extraction result persistence

**Status:** Accepted
**Scope:** COD-191 structured AI text extraction

## Context

COD-191 validates a structured extraction from a `report.created.v1` event. PostgreSQL is the system of record, while Redis Streams is transport; silently logging a result and acknowledging the stream message would discard application state.

## Current boundary

Validated extraction results are stored in PostgreSQL in `report_ai_extractions`. The table stores the report ID, contract and taxonomy versions, and the schema-validated structured result as JSONB; it deliberately does not duplicate report text. `(report_id, contract_version)` is the idempotency key.

The extraction transaction inserts the durable result and a minimal `report.ai_processed` outbox event containing `report_id`, `extraction_id`, and `contract_version`, then commits before the worker acknowledges Redis. A redelivery first checks the unique durable result and acknowledges without invoking the provider again. A failed transaction is not acknowledged.

`report.ai_processed.v1` remains a report event on the report stream. The incident-processing consumer uses a separate consumer group, loads the authoritative extraction from PostgreSQL, and emits incident-domain events through the transactional outbox.

The production worker uses the PostgreSQL-backed observer. `LoggingObserver` remains only as a compatibility test boundary and is not production wiring.
