# Location normalization contract v0

This contract transforms the extracted `location_reference` text into textual candidates for later location resolution. It is evidence interpretation only: it does not geocode, call a map provider, produce coordinates, or infer unsupported administrative precision.

## Output

- `location_state` preserves the three-way uncertainty boundary: `identified`, `ambiguous`, or `unknown`.
- `source_location_reference` preserves the extraction field exactly, including the original value, candidates, and evidence quotes.
- `candidates` contains textual candidates only. Each candidate keeps `reported_text`, a conservative `normalized_text`, a lexical `reference_kind`, an optional wording `qualifier`, and literal evidence quotes.

`normalized_text` may remove only wording that is separately represented as a qualifier or a leading article. It must not add coordinates, a street address, a landmark, or an administrative area. `named_place` means only that the report contains a place-like name; it is not a geocoding match.

## Evaluation

`evaluation.json` is the versioned deterministic evaluation manifest. It reuses location references from the v0 extraction fixtures where possible and adds direct fixtures for similar names and conflicting wording.

Run the CI-safe regression with:

```text
go test ./internal/ai/location/evaluation -run TestVersionedV0LocationNormalizationSuite -count=1 -v
```

The evaluator injects a provider and validates provider output against this schema before field-by-field comparison. It requires no network, map provider, OpenAI credentials, PostgreSQL, Redis, or reporter/device coordinates.
