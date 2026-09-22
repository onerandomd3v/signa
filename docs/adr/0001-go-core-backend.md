# ADR-0001: Go as the core backend language

- Status: Accepted
- Date: 2026-09-22

## Context

Signa needs one backend foundation for its HTTP API, workers, deterministic domain rules, persistence coordination, event processing, realtime behavior, and delivery orchestration. The MVP should keep operational complexity and language boundaries small while supporting concurrent, retryable work.

## Decision

Go is the core backend language for Signa. Go owns the HTTP API, workers, deterministic domain rules, persistence coordination, event processing, realtime server behavior, and delivery orchestration. Next.js/TypeScript remains the frontend.

The MVP will not introduce a separate Python backend or service merely because AI is involved. Specialized ML services may be reconsidered later only through an explicit architecture decision.

## Rationale

Go fits Signa's concurrency and network-service needs, including workers, SSE, and delivery fan-out. It provides predictable deployment, explicit domain and backend behavior, and a small operational footprint without adding a second backend runtime.

## Consequences

- AI integrations are orchestrated from Go through explicit, structured contracts.
- Backend implementation and operational tooling remain centered on one runtime.
- A future specialized ML service requires a separate architecture decision and an explicit contract.
