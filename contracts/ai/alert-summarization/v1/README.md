# Safe alert summarization v1

`signa.ai.alert-summarization.v1` formats an already-authoritative `signa.alert-summary-snapshot.v1` for people. The snapshot includes the internal incident ID for binding/audit, but it is never rendered as public text. It does not decide alert eligibility, confidence, severity, lifecycle, or delivery.

The input snapshot supplies only public-safe/generalized location context, canonical `event_type`, structured freshness (`last_signal_at`, `age_seconds`), `as_of`, and applicable policy versions. Exact reporter coordinates, identity, internal scores, and provider confidence are not part of this contract. Supplied timestamps and ages must agree. The output repeats the authoritative state so consumers can validate that wording and metadata refer to the same snapshot.

Canonical event types are lowercase taxonomy identifiers. `unknown` means no supported event classification is available; `other` means a discernible event outside the taxonomy. The snapshot version is `signa.alert-summary-snapshot.v1`. Public location status is only `identified` or `unknown`; freshness status is only `known` or `unknown`. Freshness is not reclassified by this contract: `age_seconds` and `last_signal_at` carry the caller-supplied temporal context, and when an age is supplied it must match `as_of` and `last_signal_at`.

Required safety behavior:

- `UNVERIFIED`, `EMERGING`, and `DISPUTED` wording remains explicitly qualified.
- `DISPUTED` wording describes conflicting information and never presents the incident as confirmed.
- Older timestamps and `RESOLVING`, `RESOLVED`, or `EXPIRED` lifecycle states must not imply a current ongoing event. The summarizer does not derive a stale/aging threshold.
- Unknown severity, freshness, or location remains unknown.
- Providers cannot alter authoritative fields; the processor binds them back and rejects mismatches.
