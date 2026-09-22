# ADR-0006: Server-Sent Events for foreground realtime updates

- Status: Accepted
- Date: 2026-09-22

## Context

An open Signa web application needs timely server-to-client updates for incident and verification state. The MVP does not require a general-purpose bidirectional realtime protocol for this flow.

## Decision

Signa will use Server-Sent Events (SSE) for foreground browser realtime updates while the application is open. The Go API serves the stream, and clients reconnect and reconcile against server-authoritative state when necessary.

SSE is for delivery of updates, not durable state. Updates originate from durable transitions and may be replayed or reloaded safely.

## Consequences

- The browser receives a simple HTTP-based, server-to-client stream.
- Connection lifecycle, heartbeat, reconnect, authorization, and backpressure behavior must be handled explicitly.
- Background or closed-app notifications remain a separate delivery concern; this ADR does not select Web Push implementation details.
