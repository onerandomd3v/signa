# Signa — Codex Repository Instructions

## Purpose

This file contains repository-wide instructions for Codex and other coding agents working on Signa.

Signa is a web-first, real-time community intelligence and safety platform. It transforms fragmented local reports into structured evidence, continuously updated incidents, and geographically relevant safety information.

Keep this file concise. Product and technical architecture belongs in `docs/architecture.md`; architecture decisions belong in `docs/adr/`; task-specific requirements belong in Linear.

More specific `AGENTS.md` or `AGENTS.override.md` files may be added inside subdirectories later when a part of the repository genuinely needs narrower instructions.

---

## Sources of truth

Use the following precedence when working on an implementation task:

1. The current Linear issue defines the task scope, assignee, acceptance criteria, blockers, and dependencies.
2. This `AGENTS.md` defines repository-wide working rules.
3. `docs/architecture.md` defines the approved system architecture.
4. `docs/adr/` records approved architecture decisions and their rationale.
5. Existing code and tests show how the approved design is currently implemented.

Do not silently resolve contradictions between these sources.

If an issue requires behavior that conflicts with the approved architecture or an ADR, surface the conflict instead of redesigning the system inside the implementation PR.

---

## Before editing

Every implementation task must be tied to a Linear issue.

Before changing code:

1. Inspect the actual Linear issue.
2. Inspect its blocker/dependency relationships.
3. Do not implement an issue that is currently blocked.
4. Read the relevant architecture/ADR documentation.
5. Inspect the existing implementation before deciding what to change.
6. Keep the implementation inside the issue's acceptance criteria.

Do not invent additional product requirements because they seem useful.

---

## Git workflow

The integration branch is:

`dev`

The stable/production branch is:

`main`

Normal work must follow:

```text
latest origin/dev
        ↓
short-lived issue branch
        ↓
implementation + tests
        ↓
pull request into dev
        ↓
owner review
        ↓
owner merge
```

Never implement directly on `dev` or `main`.

Before starting a task, fetch the latest remote state and branch from current `origin/dev`.

Use one branch for one coherent Linear issue.

Use the Linear-recommended branch name when appropriate. Otherwise use a clear branch name containing the Linear issue identifier and short purpose.

Examples:
```text
feat/COD-187-report-ingestion
fix/COD-217-web-push-delivery
docs/COD-179-architecture-adrs
test/COD-192-ai-extraction-evals
chore/COD-245-repository-baseline
```

Do not bundle unrelated Linear issues into one PR.

Collaborators must not merge protected-branch pull requests. Submit PRs into `dev` and request review from the repository owner.

The GitHub repository owner, `onerandomd3v`, performs final merges for collaborator and owner-authored pull requests. Required automated checks must pass before merge. The owner may merge owner-authored PRs without self-review using the configured owner ruleset permissions. Only the repository owner may use the configured bypass; collaborators must not bypass protected-branch rules.

All normal implementation PRs target `dev`.

Promotion to `main` happens separately through the protected release workflow.

Never force-push `dev` or `main`.

---

## Pull requests

PR titles should reference the Linear issue.

Preferred format:
```text
COD-123: concise change description
```

The PR description should identify:

- the Linear issue;
- what changed;
- why;
- how it was tested;
- architecture/data implications when applicable.

Keep PRs focused and reviewable.

If implementation discovers work outside the current issue, do not silently include it. Surface it as a follow-up or dependency.

---

## Canonical repository topology

The approved repository direction is defined in `docs/architecture.md`.

Use these canonical locations:
```text
apps/web/           Next.js + TypeScript frontend

cmd/api/            Go API executable
cmd/worker/         Go worker executable

internal/           Go domain/application modules

contracts/          versioned API and AI contracts
contracts/openapi.yaml
                    HTTP API contract when created

migrations/         PostgreSQL/PostGIS migrations

docs/               project technical documentation
docs/architecture.md
                    architecture source of truth
docs/adr/           architecture decision records

deploy/             deployment/runtime configuration
```

Do not invent parallel structures such as:
```text
/frontend
/client
/web-app
/backend
/server
/api-service
/ai-service
```

unless an approved architecture change explicitly introduces them.

Git does not track empty directories. Do not add meaningless `.gitkeep` files merely to pre-create the entire future repository shape.

Create a canonical directory when the Linear issue that owns that implementation needs it.

---

## Primary ownership lanes

Primary engineering ownership is:
```text
Backend / platform / infrastructure
onerandomd3v (onerandomdev)

Frontend
Thundey

AI / intelligence layer
Saheed
```

Ownership is a coordination boundary, not a prohibition on touching another directory.

However, do not implement substantial work belonging to another lane merely to complete your current issue.

If your issue genuinely requires a non-trivial change in another owner's area, surface the dependency and keep cross-lane changes minimal and explicit.

---

## Architecture guardrails

The core architecture is defined in `docs/architecture.md`.

Repository changes must preserve these principles unless an approved ADR changes them.

### AI and system decisions

AI interprets evidence.

AI does not independently decide truth or directly control alert delivery.

AI may extract, classify, summarize, transcribe, identify similarity, detect contradictions, and produce evidence signals.

Deterministic application rules own confidence transitions, severity policy, P1/P2/P3 priority, and alert eligibility.

### Separate concepts

Do not collapse these concepts into one opaque score:
```text
Report Evidence
Incident Confidence
Incident Severity
User Priority
```

They have different purposes.

### Durable state

PostgreSQL/PostGIS is the authoritative durable system of record.

Redis/Redis Streams is not the source of truth for incidents.

Critical asynchronous operations must be retryable and idempotent where applicable.

### Geography

Geography is a first-class domain concern.

Use PostGIS for spatial relationships rather than reproducing core spatial logic with ad-hoc application calculations.

### Privacy

Exact reporter location and sensitive identity information must not be exposed through public/client-facing APIs unless explicitly required by an approved design.

Collect and retain only location precision required for the feature.

### Reporting

Reporting remains text-first.

Optional image/audio/video processing must not prevent the initial text report from being accepted and persisted.

### Processing

Expensive AI/media/downstream processing should remain asynchronous where architecture specifies it.

A downstream AI/provider failure must not erase an already accepted report.

### System shape

The MVP begins as a modular monolith with workers.

Do not introduce microservices, Kafka, Kubernetes, a second backend architecture, or other major infrastructure without an approved architecture decision.

---

## Contracts

The frontend and Go backend communicate through explicit contracts.

When an HTTP API contract exists, keep implementation and `contracts/openapi.yaml` aligned.

Do not hand-maintain conflicting frontend API types when generated contract types are available.

AI outputs that feed application logic should use versioned structured schemas instead of free-form prose.

Unknown AI information must remain unknown; do not fabricate missing locations, timestamps, actors, or events.

---

## Testing and validation

Every implementation PR must test changed behavior at the appropriate level.

Run all relevant repository-defined checks before opening or updating a PR.

Depending on the area, this may include:
```text
Go tests
Go lint/static checks
frontend lint
frontend typecheck
frontend tests
OpenAPI validation
database migration tests
AI schema tests
AI evaluation fixtures
integration tests
```

Use the commands defined by the repository at the time of the issue.

Do not invent fake passing commands for tooling that has not been initialized yet.

If a required check cannot be run, state that clearly in the PR instead of claiming it passed.

Bug fixes should include a regression test where practical.

New behavior should cover meaningful failure/error states, not only the happy path.

---

## Database and asynchronous processing

When changing durable state:

- use migrations;
- preserve transactional correctness;
- make migrations reviewable and reversible where practical;
- add indexes required by expected query patterns.

When handling queued/streamed events:

- assume an event may be delivered more than once;
- make handlers idempotent where required;
- acknowledge work only after the durable operation succeeds;
- preserve retry/failure visibility.

Do not create reliability assumptions that depend on exactly-once network delivery.

---

## Frontend rules

The browser renders server-authoritative domain state.

Do not make the frontend authoritative for:
```text
incident confidence
severity
evidence independence
P1/P2/P3 priority
alert eligibility
incident resolution
reporter reputation
```

User-facing flows should handle relevant loading, success, empty, denied-permission, and error states.

Maintain responsive/mobile usability and accessibility for core workflows.

Do not tell a user an area or route is "safe" merely because Signa has no verified incident there.

---

## AI rules

AI changes must preserve uncertainty.

Prefer structured output and versioned fixtures/evaluations.

Include representative difficult inputs when relevant, including Nigerian Pidgin, slang, incomplete reports, hearsay, vague places, relative time expressions, contradictory observations, and duplicate content.

AI agreement is not independent corroboration.

Media type is not proof.

Do not allow AI output alone to bypass deterministic system rules.

---

## Security and secrets

Never commit:
```text
.env
.env.local
API keys
provider secrets
private keys
service-account credentials
production credentials
tokens
```

Use environment variables and an `.env.example` containing variable names/placeholders only when configuration is introduced.

Keep object storage private by default.

Avoid logging sensitive reporter information or precise location unless required for controlled debugging.

Do not expose secrets in tests, fixtures, documentation, screenshots, PR descriptions, or logs.

---

## Dependencies and architecture changes

Do not add a major production dependency merely because it makes an issue easier.

A dependency introduced by an issue should have a clear purpose and fit the approved architecture.

Changes involving the following require explicit architecture review before being silently implemented:
```text
primary language/framework
database choice
event backbone
AI provider architecture
service boundaries
confidence model
P1/P2/P3 semantics
privacy/location model
authentication architecture
routing architecture
deployment architecture
```

When approved, record significant decisions as ADRs.

---

## Documentation

Update documentation when an issue changes public contracts, architecture, developer setup, or operational behavior.

Do not copy the full architecture into this file.

Keep detailed design in `docs/architecture.md` and rationale in `docs/adr/`.

---

## Code Review Rules

When reviewing changes, prioritize correctness and product-safety risks over formatting preferences.

Flag:

- divergence from the Linear issue;
- architecture violations;
- AI making deterministic truth/alert decisions;
- client code becoming authoritative for server domain state;
- exposure of precise reporter location or sensitive identity;
- secrets or credentials in the repository;
- missing idempotency in retryable processing;
- API/schema drift;
- untested material behavior;
- unsafe alert wording or unsupported certainty;
- unrelated refactors that enlarge the PR unnecessarily.

Prefer CI for mechanical formatting/lint enforcement.

---

## Completion standard

An issue is implementation-complete only when:

1. its acceptance criteria are satisfied;
2. relevant tests/checks have passed or limitations are disclosed;
3. required documentation/contracts are updated;
4. no unrelated work has been bundled into the PR;
5. a reviewable PR targeting `dev` exists;
6. the Linear issue is referenced.

The repository owner performs the final merge.
