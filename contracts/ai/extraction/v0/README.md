# AI report extraction contract v0

This directory defines the versioned, structured interpretation that a later worker may produce from one raw report. It is a contract only: it does not call a model, verify an event, cluster reports, or implement product policy.

## Files and versions

- `schema.json` is the JSON Schema Draft 2020-12 contract. The output pins both `contract_version` (`signa.ai.report-extraction.v0`) and `taxonomy_version` (`signa.event-taxonomy.v0`).
- `event-taxonomy.json` defines the conservative MVP event identifiers and boundaries. Keep its identifiers aligned with the event enum in `schema.json`. Add a new version rather than silently changing the meaning of a published version.
- `fixtures/` contains raw report examples and explicit expected extraction outputs for later implementation and evaluation work.

## Field and uncertainty semantics

Every extracted field uses the same object: `status`, `value`, `candidates`, and `evidence_quotes`.

- `identified`: `value` contains one candidate, `candidates` is empty, and at least one supporting excerpt is retained.
- `ambiguous`: `value` is `null`, `candidates` contains at least two plausible values, and supporting excerpt(s) are retained.
- `unknown`: `value` is `null`, and both arrays are empty. Do not guess to fill a gap.

Quotes are literal excerpts from the raw report, not generated explanations. An ambiguous or unknown field must not be promoted to an identified value downstream without new evidence or an explicit separate process.

`event_type` candidates use only the v0 taxonomy IDs. `other` means a discernible event outside the small taxonomy; `unknown` means the report does not support an event classification. A question, rumor with no discernible event, or vague statement can therefore have an unknown event type.

## Source claim and language

`source_claim` is an interpretation of wording, not a measure of credibility or independence:

- `first_hand`: the reporter claims direct observation;
- `second_hand`: the reporter says the information came from another person;
- `forwarded`: the report explicitly indicates repeated/forwarded material; prefer this more specific label over generic second-hand wording, even if the original source is unknown;
- `unclear`: the wording explicitly says provenance is unknown/unclear, without an explicit forwarding/repetition claim.

If the report gives no provenance clue, use `unknown`, not `unclear`. Multiple AI labels or repeated copies are not independent sources and must not become a confidence score.

`language` uses stable identifiers: `en` (standard English), `pcm` (Nigerian Pidgin), and `en-pcm` (mixed English and Nigerian Pidgin). Use the shared `unknown` state when language is undetermined; do not substitute a guessed language.

## Location and time

`location_reference.value` and `time_reference.value` preserve the wording expressed in the report. Derive them only from that report text—not device/GPS metadata or external context. Keep useful raw phrases such as `near market`, `this morning`, or `since yesterday`; do not geocode, invent coordinates, resolve a place, or convert relative wording into an exact timestamp here. Use `ambiguous` when multiple references remain plausible and `unknown` when none is stated. Location resolution belongs to later GEO-05 / COD-207 work.

## Severity boundary

`severity_candidate` uses the architecture's conceptual `LOW`, `MODERATE`, `HIGH`, and `CRITICAL` vocabulary. It is only the interpretation: “If this reported event were occurring as described, what seriousness class does the report appear to suggest?” It is not final incident severity, confidence, truth, or user priority.

Do not use source/provenance or claim-certainty wording as severity evidence: uncertainty about whether an event occurred is distinct from how serious it would be if occurring as described. Base any severity candidate on the described event and consequences; leave it unknown when those details are insufficient.

AI extraction is evidence interpretation, not verification. AI output alone cannot:

- mark an incident true or transition its lifecycle;
- set final incident confidence;
- set final severity policy;
- determine P1/P2/P3;
- determine alert eligibility or send an alert.

Those decisions remain with deterministic backend/domain policy. Missing actors, causes, provenance, certainty, locations, times, and event types remain missing; do not add coordinates, names, or precision unsupported by the report.

## Validation scope

The repository currently has no JSON Schema validator or contract test tooling. JSON parsing and fixture/taxonomy consistency can be checked with available standard-library tools, but those checks are not a substitute for validating the schema against a JSON Schema implementation. COD-191 should add/use the repository's chosen validator when worker implementation begins.
