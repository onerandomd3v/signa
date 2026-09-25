# Pilot AI benchmark v1

`evaluation.json` composes the existing versioned fixture suites for extraction, location interpretation, same-event similarity, contradiction, evidence independence, and safe alert summarization. It reuses their schemas, expected outputs, tolerance rules, and evaluator implementations; it does not duplicate or redefine component semantics. COD-224 coordination is run and reported separately as a dependency guardrail, outside the six-component overall denominator.

Run the deterministic local/CI regression from the repository root:

```text
go run ./cmd/pilot-benchmark
```

The command requires no network, OpenAI credential, PostgreSQL, or Redis. Location, similarity, contradiction, independence, coordination, and alert summarization use their existing deterministic implementations. Extraction has no offline extractor implementation, so the default runner replays the checked-in expected fixture outputs; its `fixture_replay` mode is reported explicitly and is not model-accuracy evidence. Callers can inject alternative component providers through the Go runner API; injected mode is recorded in the output.

The report uses `signa.ai.pilot-benchmark-report.v1` and conforms to `report.schema.json`. It includes component and guardrail evaluation/contract versions, fixture-case counts, evaluator-unit counts (similarity may evaluate multiple candidates within one fixture), pass rates, failed case IDs/categories and sanitized failure kinds, scenario coverage, and known limitations. No input text, reporter identity, coordinates, opaque origins, media fingerprints, or provider error contents are emitted.

`acceptance_threshold_status` is `not_configured`. The overall result reports fixture pass/fail and pass rate only; it does not gate on a numeric product threshold. COD-230's explicit threshold API is not used by this benchmark.

## Scenarios covered

The versioned manifest maps synthetic checked-in fixtures to standard English, Nigerian Pidgin, mixed English/Pidgin, slang, incomplete reports, hearsay/forwarded claims, vague/ambiguous places, relative time wording, duplicate/near-duplicate evidence, conflicting reports, sparse provenance, high-severity/low-confidence wording, disputed evidence, and unknown location/freshness/severity.

## Known limitations

The benchmark's limitation list is embedded in each stable JSON report. In particular, fixture replay does not assess extraction-model quality; location normalization is rule-based and does not geocode; similarity is uncalibrated and not truth; independence and media-duplication signals require the supported opaque evidence inputs; contradiction and coordination cover only versioned supported patterns; and fixture performance is not real-world accuracy.
