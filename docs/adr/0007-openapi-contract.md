# ADR-0007: OpenAPI as the Go and TypeScript API contract

- Status: Accepted
- Date: 2026-09-22

## Context

The Go backend and Next.js/TypeScript frontend need a stable boundary for endpoints, schemas, enums, errors, and authentication requirements. Sharing implementation-language types directly would couple the two applications.

## Decision

Signa will define its canonical HTTP API contract at `contracts/openapi.yaml`. The Go backend and TypeScript frontend align to this contract for request and response schemas, error shapes, enums, and authentication requirements. Generated TypeScript types and clients should be used where practical; conflicting manually maintained frontend API types should be avoided. Implementation changes must keep the contract aligned.

## Consequences

- Public HTTP behavior is reviewed as a versioned contract rather than inferred from handlers.
- Contract validation belongs in the relevant quality gates once the API is initialized.
- COD-184 owns creation of the contract pipeline; this ADR does not create `contracts/openapi.yaml`.
