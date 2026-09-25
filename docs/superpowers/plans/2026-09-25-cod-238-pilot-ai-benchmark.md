# COD-238 Pilot AI Benchmark Implementation Plan

> **For agentic workers:** Use the native Codex implementation workflow. Each task ends with a focused test cycle.

**Goal:** Run the six existing versioned AI fixture suites plus the COD-224 coordination guardrail and emit a deterministic, privacy-safe aggregate report without inventing an acceptance threshold.

**Architecture:** Add a thin orchestration package in `internal/ai/pilot/evaluation` that loads existing suite manifests and schemas, invokes their existing evaluators with injectable providers, and normalizes their reports. A small command in `cmd/pilot-benchmark` will use deterministic local providers and print stable JSON; extraction uses an explicitly labeled expected-fixture replay because there is no local extraction implementation.

**Tech Stack:** Go, existing AI evaluation packages, checked-in JSON manifests and JSON Schema; no new dependencies or external services.

**Spec:** COD-238 Linear issue and the user-provided task brief.

## Global Constraints

- The checked-in component fixtures are the authoritative oracle.
- No numeric acceptance threshold is configured; report fixture counts and pass rate only.
- Keep provider outputs injectable; the default command is offline and deterministic.
- Do not change extraction, location, similarity, contradiction, independence, coordination, or alert-summary semantics.
- Keep reporter/device coordinates, identity, opaque origins, media fingerprints, and secrets out of output.
- The coordination suite is reported as a dependency guardrail, separate from the six-component benchmark denominator.

## Review Focus

- Extraction replay can look like model accuracy: label its mode as fixture replay and disclose this limitation.
- Similarity counts candidate comparisons, not only fixtures: report both fixture-case counts and evaluator-unit counts.
- Component diagnostics can echo provider data: aggregate only safe failure kinds, case IDs, and checked-in categories.
- Empty or inconsistent evaluator reports must fail safely rather than produce misleading pass rates.
- Coverage links must resolve to actual cases in the referenced manifests.

### Task 1: Version the pilot benchmark manifest and report contract

**Files:** Create `contracts/ai/pilot-benchmark/v1/evaluation.json`, `README.md`, and `report.schema.json`.

- [ ] Declare all six benchmark components, the separate coordination guardrail, component manifest/schema paths, scenario-to-fixture coverage, threshold status `not_configured`, and known limits.
- [ ] Define a report schema with stable component counts, case-level failures, pass rate, provider mode, scenario coverage, and known-limit fields.
- [ ] Add loader tests in `internal/ai/pilot/evaluation/evaluation_test.go` for required components, valid coverage references, and malformed manifest/schema rejection.

### Task 2: Orchestrate existing evaluators with injectable providers

**Files:** Create `internal/ai/pilot/evaluation/evaluation.go` and focused helpers as needed.

- [ ] Define `Providers` fields using the existing package interfaces and a `Run(ctx, repoRoot, providers)` entry point.
- [ ] Load each existing suite and canonical schema, then delegate scoring/comparison to its existing evaluator.
- [ ] Use `location.RuleBasedNormalizer`, `similarity.RuleBasedScorer`, `contradiction.RuleBasedEvaluator`, `independence.RuleBasedEvaluator`, `coordination.RuleBasedEvaluator`, and deterministic alert rendering when providers are not injected.
- [ ] Implement a local extraction expected-output replay provider and label it `fixture_replay`; never call OpenAI or the network by default.
- [ ] Normalize evaluator results into per-component case counts, evaluator-unit counts, failure case IDs/categories, versions, and pass rates. Keep coordination out of the six-component overall denominator.
- [ ] Validate result counters and failure case IDs; malformed reports return errors. Sanitize diagnostics to failure kinds so raw text, tokens, coordinates, origins, and fingerprints cannot enter the report.
- [ ] Test deterministic repeated output, correct aggregation, injected component failure attribution, privacy-safe errors, explicit threshold-not-configured status, and malformed-result rejection.

### Task 3: Add the local/CI regression command and documentation

**Files:** Create `cmd/pilot-benchmark/main.go`; update the pilot-benchmark README and add tests for the CLI/report schema.

- [ ] Implement `go run ./cmd/pilot-benchmark` to find the repository root, run the offline benchmark, print stable JSON, and return non-zero only when a fixture comparison fails or execution is malformed.
- [ ] Document provider modes, component versions, categories, threshold status, known limitations, and the single regression command.
- [ ] Validate report JSON against `report.schema.json` in tests.
- [ ] Run all requested targeted suite regressions, `go test ./internal/ai/... -count=1`, `go test ./...`, `go vet ./...`, `golangci-lint run`, and `git diff --check`; disclose Windows Application Control or missing-tool limitations exactly.

---
