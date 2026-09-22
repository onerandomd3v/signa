# ADR-0008: Separate AI interpretation from deterministic decisions

- Status: Accepted
- Date: 2026-09-22

## Context

AI is useful for extracting meaning from noisy reports, but model output is uncertain and is not independent corroboration. Signa requires explainable, auditable rules for confidence, severity, priority, and alert eligibility.

## Decision

AI interprets evidence and returns versioned structured signals such as event, location, time, language, similarity, contradiction, and candidate severity information. Deterministic application rules own confidence transitions, severity policy, P1/P2/P3 priority, alert eligibility, and durable state transitions.

AI output must not by itself mark an incident true, bypass thresholds, or send a public alert. Unknown information remains unknown, and downstream provider failure must not erase an accepted report.

## Consequences

- AI contracts and fixtures preserve uncertainty and explicit unknown states.
- Rule versions and relevant evidence are auditable with important transitions.
- AI processing remains asynchronous where appropriate and can be retried independently.
- Future changes to the confidence, severity, priority, or alert policy require explicit product or architecture decisions as applicable.
