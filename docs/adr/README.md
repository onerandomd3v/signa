# Architecture Decision Records

Architecture Decision Records (ADRs) capture significant decisions, their context, and their consequences. Add an ADR when a decision commits the project to an important architectural direction.

Use a sequential four-digit prefix and a short kebab-case topic, for example:

```text
0001-go-core-backend.md
0002-postgresql-postgis.md
```

Do not renumber accepted ADRs. Supersede a decision with a new ADR that links to the earlier record.

## Accepted ADRs

- [0001 — Go as the core backend language](0001-go-core-backend.md)
- [0002 — Chi v5 as the Go HTTP router](0002-chi-http-router.md)
- [0003 — PostgreSQL/PostGIS as system of record](0003-postgresql-postgis-system-of-record.md)
- [0004 — Redis Streams as MVP event backbone](0004-redis-streams-event-backbone.md)
- [0005 — Transactional outbox](0005-transactional-outbox.md)
- [0006 — SSE for foreground realtime](0006-sse-foreground-realtime.md)
- [0007 — OpenAPI contract](0007-openapi-contract.md)
- [0008 — AI interpretation vs deterministic decisions](0008-ai-interpretation-deterministic-decisions.md)
- [0009 — Location minimization](0009-location-minimization.md)
- [0011 — OSRM-compatible routing adapter](0011-routing-provider.md)

## Open architecture blockers

- [0010 — AI extraction result destination blocker](0010-ai-extraction-result-destination-blocker.md)
