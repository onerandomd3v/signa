# ADR-0002: Chi v5 as the Go HTTP router

- Status: Accepted
- Date: 2026-09-22

## Context

The architecture left the Go HTTP routing choice between the standard library and a thin router. Signa needs route grouping and composable middleware without coupling domain code to a heavyweight framework.

## Decision

Signa will use Chi v5, `github.com/go-chi/chi/v5`, for Go HTTP routing. Handlers and middleware must remain compatible with standard `net/http`, and domain packages must not depend on Chi-specific types or behavior.

This ADR selects the router only. It does not initialize the Go module or define application startup; COD-182 owns application initialization.

## Rationale

Chi provides lightweight routing, route grouping and composition, and middleware composition while preserving standard `net/http` compatibility. It is a good fit for a modular monolith and keeps architectural lock-in low.

## Consequences

- New routes use Chi's router and middleware composition.
- HTTP handlers can remain straightforward `net/http` handlers and are easy to test.
- Replacing the router later should not require changes to domain or application logic.
