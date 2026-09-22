# ADR-0009: Location minimization and approximate public geography

- Status: Accepted
- Date: 2026-09-22

## Context

Location is necessary for relevance, proximity, route, and affected-area calculations, but exact reporter coordinates and continuous movement can expose sensitive information. Safety features must not require retaining more location data than their purpose needs.

## Decision

Signa collects and retains only the location precision and duration required for a feature. Exact browser or reporter location is restricted to controlled server-side relevance and spatial processing. Public-facing incident information uses an appropriate generalized area or zone and never exposes reporter coordinates by default.

Location consent is explicit. Continuous background tracking is not part of the MVP default, and route data is short-lived unless a clearly approved feature requires retention.

## Consequences

- APIs and client views must not expose exact reporter coordinates unless an approved design explicitly requires it.
- PostGIS may use protected precise data for spatial relationships while public projections use generalized geometry.
- Retention and access controls are part of the feature's design, not an afterthought.
- Features that need additional precision, duration, or continuous tracking require explicit review and an architecture decision where the boundary changes.
