# Source coordination v1

`signa.ai.source-coordination.v1` combines pairwise evidence-repetition/provenance signals from the COD-199 independence evaluator with supplied submission timestamps. It is an interpretation signal only: it does not establish coordination as fact, incident truth, incident confidence, severity, priority, attachment, or alert eligibility.

## Input and configuration

The evaluator accepts at least two observations with distinct evidence IDs. Each observation contains COD-199 evidence and may contain a `submitted_at` timestamp. It consumes no reporter identity, device, IP address, style, or other identity proxy. Source-origin values and media fingerprints are opaque comparison tokens and are never returned.

Every invocation requires `config_version: signa.ai.source-coordination-config.v1` and a positive `synchronization_window_seconds`. There is no product default. The exact configuration is bound into output. Timing is `unknown` unless every observation has a supplied timestamp; vague extracted time language is not converted into submission time.

## Output

Five factors expose outcome, counts, weight, and a plain-language reason: `text_similarity`, `media_fingerprint`, `source_origin`, `source_claim`, and `submission_timing`. Pairwise signals reuse the versioned COD-199 word-set Jaccard method and its 0.8 near-duplicate cutoff. `pair_count`, `known_pair_count`, and `supporting_pair_count` make aggregation visible.

Weights are per-factor raw descriptors, never a combined score or calibrated probability. Text weight is the maximum COD-199 Jaccard similarity among pairs classed as repetition risk, or zero when known pairs are all below its versioned cutoff; it is null when text comparisons are unavailable. Other evidence-factor weights are the proportion of known pairs with an independence or repetition signal; timing weight is 1 for synchronized, 0 for unsynchronized, and null when unknown. Missing evidence has a null weight. These values do not encode incident confidence.

`possible_coordination` is emitted only when every supplied submission timestamp falls within the explicitly configured window and at least one pairwise factor reports repetition risk. Known timestamps outside the window yield `no_signal` for this configured burst heuristic; missing timestamps yield `indeterminate`, even if repeated content is detected. Synchronized input with no positive repetition signal but unknown factor evidence also remains `indeterminate`. Synchronization alone does not imply coordination. Different wording/media alone does not imply independent sources. Distinct source origins retain COD-199's limited `independence_support` semantics and do not negate repeated content.

## Evaluation

The manifest pins evaluation, contract, and tolerance versions. Weights are compared with the manifest's explicit tolerance; states, outcomes, and counts are exact. Fixtures cover exact/near duplicate text, media, same/distinct origin, forwarded claims, synchronized and unsynchronized timing, mixed evidence, sparse provenance, absent timestamps, and multiple observations.

Run the deterministic, offline regression suite with:

```sh
go test ./internal/ai/coordination/... -count=1
```
