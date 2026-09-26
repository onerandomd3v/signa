# ADR-0011: OSRM-compatible routing adapter for request-scoped route geometry

- Status: Accepted
- Date: 2026-09-25

## Context

Signa needs route geometry for future route/incident spatial operations, but the MVP should not operate its own routing infrastructure. Routing-provider details must remain behind a provider-independent internal boundary, and exact user locations and route geometry must not become durable or public by default.

## Decision

The initial routing provider is an OSRM-compatible HTTP endpoint selected through `SIGNA_ROUTING_PROVIDER=osrm` and an explicitly configured `SIGNA_ROUTING_BASE_URL`. `internal/routing` owns the provider adapter and requests GeoJSON `LineString` geometry. `SIGNA_ROUTING_TIMEOUT` is required so every request has an explicit deadline.

The adapter validates coordinates and returned geometry, limits response size, supports cancellation, and returns safe typed error categories without copying upstream bodies or sensitive coordinates into errors or logs. Route results remain request-scoped; COD-208 does not persist route geometry or publish it through events.

COD-253 adds a separate authenticated `POST /v1/route-relevance` browser projection. That operation may return the validated provider `LineString` to the authenticated requester for that request's map display only. The projection does not persist route input or geometry, publish an event, expose provider metadata, or change COD-210 priority evaluation, audit behavior, reasons, or events.

## Consequences

- OSRM-compatible protocol details can be replaced behind the `Provider` interface without changing route consumers.
- Operators must supply a private, approved routing endpoint; no public provider URL is hardcoded.
- Provider availability and response quality remain external dependencies and require operational monitoring.
- Alternative routes, route safety recommendations, and route persistence remain outside this decision. COD-253 owns the browser-facing request-scoped route relevance contract; it must not be interpreted as a route safety guarantee.
