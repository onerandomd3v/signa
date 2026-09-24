# Contradiction assessment v1

This contract reports a narrow, auditable contradiction signal between two
explicit claims. It is not a truth or verification decision, incident
confidence, lifecycle transition, severity, priority, alert decision, or
durable incident update.

The deterministic evaluator recognizes only same-event/same-identified-place
claim pairs with supported opposite states: passage `blocked` vs `open`, and
activity `ongoing` vs `stopped`. Unknown or ambiguous context stays
indeterminate; different identified event or location context is
`not_comparable`. A provider can be injected to interpret richer claim input,
but its output must satisfy the same contract and factor/state invariants.

`recency_order` compares only caller-supplied observation timestamps. It has no
staleness threshold and does not convert natural-language time references.
Missing timestamps remain `unknown`.

Run the deterministic regression suite locally or in CI with:

```sh
go test ./internal/ai/contradiction/... -count=1
```
