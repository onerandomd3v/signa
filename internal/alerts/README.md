# Alerts

`internal/alerts` owns the deterministic `signa.alert-eligibility.v1` policy and durable alert snapshots.

| Input | Eligible behavior |
| --- | --- |
| `P1` + fresh active incident + `HIGH`/`CRITICAL` + actionable confidence | `IMMEDIATE` |
| `P2` + fresh active incident + `MODERATE`/`HIGH`/`CRITICAL` + actionable confidence | `NEARBY` |
| `P3` or `NONE` | Ineligible for realtime alert creation |
| `UNVERIFIED` or `DISPUTED` confidence | Ineligible |
| `STALE` or `UNKNOWN` freshness | Ineligible |
| `RESOLVED` or `EXPIRED` status | Ineligible |

The store accepts a message only after the caller has applied the existing safe-summary boundary. It does not call AI, choose a delivery channel, or send notifications. Eligible creation persists an immutable snapshot and an `alert.created.v1` outbox event in one PostgreSQL transaction. A unique idempotency key returns the canonical existing alert on retry.

Delivery is a separate worker concern. The provider-neutral consumer boundary consumes `delivery.requested.v1` only when a real channel adapter is configured, reloads the durable delivery and alert snapshot, and skips superseded alerts. It never exposes exact reporter location or other private alert inputs in the stream payload.

The authenticated `GET /alerts/{alert_id}` read path authorizes visibility through the authenticated principal's durable delivery relationship and returns the immutable stored snapshot. A `STALE`, superseded, `RESOLVING`, `RESOLVED`, or `EXPIRED` snapshot is not rewritten or treated as current; the browser receives those stored lifecycle/freshness values so the COD-214 safety semantics remain intact. Missing or terminally undisclosed delivery visibility returns `404` to avoid confirming alert existence.
