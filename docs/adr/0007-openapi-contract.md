# ADR-0007: OpenAPI as the Go and TypeScript API contract

- Status: Accepted
- Date: 2026-09-22

## Context

The Go backend and Next.js/TypeScript frontend need a stable boundary for endpoints, schemas, enums, errors, and authentication requirements. Sharing implementation-language types directly would couple the two applications.

## Decision

Signa will define its HTTP API with OpenAPI. The contract is the source for request and response schemas, error shapes, enums, and authentication requirements. TypeScript client types should be generated from the contract where practical, and implementation changes must keep the contract aligned.

## Consequences

- Public HTTP behavior is reviewed as a versioned contract rather than inferred from handlers.
- Contract validation belongs in the relevant quality gates once the API is initialized.
- Frontend code must not hand-maintain conflicting API types when generated contract types are available.
