# Evidence independence v1

This contract reports explainable repetition and source-origin signals for a pair of supplied observations. It does not establish incident truth, incident confidence, evidence independence as a fact, report attachment, severity, or alert eligibility. It does not persist results.

The assessment exposes four fixed factors: normalized word-set Jaccard text similarity, optional duplicate media fingerprints, optional opaque source-origin comparison, and extraction `source_claim` provenance. Identical or near-identical wording and matching fingerprints indicate repetition risk. Distinct opaque source origins are a limited independence-support signal. Distinct media, different wording, and first-hand claims alone do not establish independence. Forwarded/second-hand claims indicate a repetition risk; unknown and unclear remain unknown. Opaque origin tokens and fingerprints are input-only and never echoed.

Integration gap: current `report_media` rows do not provide a content fingerprint. `object_key`, media size, and content type are not treated as evidence of media identity or uniqueness. Source-origin values are optional opaque inputs; this evaluator does not derive them from reporter IDs, names, device data, or writing style. No storage or ingestion integration is introduced; callers need an approved privacy-reviewed source-origin token and/or content fingerprint to use those factors.

The near-duplicate cutoff (Jaccard >= 0.8) and word-set method are versioned in the contract output and evaluation tolerance file. No calibrated numeric independence weight is produced.

Run the deterministic, network-free regression suite locally or in CI:

```powershell
go test ./internal/ai/independence/evaluation -run '^TestVersionedV1EvidenceIndependenceSuite$' -count=1 -v
```

Provider output is injectable through `independence.Provider`; `RuleBasedEvaluator` supplies a local deterministic implementation. Every evaluated output is validated against `schema.json` before field comparison.
