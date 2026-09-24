# Safe alert summarization v1

`signa.ai.alert-summarization.v1` formats an already-authoritative incident snapshot for people. It does not decide alert eligibility, confidence, severity, lifecycle, or delivery.

The input snapshot supplies only public-safe/generalized location context. Exact reporter coordinates, identity, internal scores, and provider confidence are not part of this contract. The output repeats the authoritative state so consumers can validate that wording and metadata refer to the same snapshot.

Required safety behavior:

- `UNVERIFIED`, `EMERGING`, and `DISPUTED` wording remains explicitly qualified.
- `DISPUTED` wording describes conflicting information and never presents the incident as confirmed.
- `STALE`, `RESOLVING`, `RESOLVED`, and `EXPIRED` wording must not imply a current ongoing event.
- Unknown severity, freshness, or location remains unknown.
- Providers cannot alter authoritative fields; the processor binds them back and rejects mismatches.
