# ADR-0003: PostgreSQL and PostGIS as the system of record

- Status: Accepted
- Date: 2026-09-22

## Context

Signa needs durable, transactional state for reports, incidents, evidence, alerts, audits, and spatial relationships. Realtime and queueing infrastructure must not become a second source of truth.

## Decision

PostgreSQL is Signa's authoritative durable database. PostGIS provides first-class spatial capabilities. Incident, report, evidence, alert, audit, and other authoritative domain state belongs in PostgreSQL.

Redis must never become the authoritative source of incident state. Spatial relationships should use PostGIS rather than ad-hoc application-side geography calculations where PostGIS is appropriate.

## Consequences

- Durable domain transitions are committed transactionally in PostgreSQL.
- Spatial queries use PostGIS types, indexes, and predicates where applicable.
- Redis data is treated as derived, ephemeral, or transport state and must be rebuildable from durable state.
- PostgreSQL/PostGIS operations remain subject to the location-minimization and privacy rules in ADR-0009.
