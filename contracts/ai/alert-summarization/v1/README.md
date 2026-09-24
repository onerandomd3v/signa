# Safe alert summarization v1

`signa.ai.alert-summarization.v1` formats an already-authoritative `signa.incident-alert-snapshot.v1` for people. The snapshot includes the internal incident ID for binding/audit, but it is never rendered as public text. It does not decide alert eligibility, confidence, severity, lifecycle, or delivery.

The input snapshot supplies only public-safe/generalized location context, canonical `event_type`, structured freshness (`last_signal_at`, `age_seconds`), `as_of`, and applicable policy versions. Exact reporter coordinates, identity, internal scores, and provider confidence are not part of this contract. Supplied timestamps and ages must agree. The output repeats the authoritative state so consumers can validate that wording and metadata refer to the same snapshot.

Required safety behavior:

- `UNVERIFIED`, `EMERGING`, and `DISPUTED` wording remains explicitly qualified.
- `DISPUTED` wording describes conflicting information and never presents the incident as confirmed.
- `STALE`, `RESOLVING`, `RESOLVED`, and `EXPIRED` wording must not imply a current ongoing event.
- Unknown severity, freshness, or location remains unknown.
- Providers cannot alter authoritative fields; the processor binds them back and rejects mismatches.
