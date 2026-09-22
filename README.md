# Signa

Signa is a real-time community intelligence and safety platform that converts fragmented local reports into continuously updated, confidence-aware incidents and distributes relevant safety information to the people who need it first.

## Status

Signa is in early architecture and MVP development.

The platform addresses the gap between scattered, fast-moving local reports and the timely, trustworthy information people need to make safer decisions. Its architecture is designed to turn reports into relevant signals while preserving confidence and context.

The current technical source of truth is the [System Architecture](docs/architecture.md).

Before implementation, read the repository-wide [Codex instructions](AGENTS.md), the [ADR index](docs/adr/), and the [Signa Linear project](https://linear.app/codeddevs/project/signa-7d2f8fe71827). Linear identifies `onerandomdev` as repository owner, while GitHub identifies `onerandomd3v`; until reconciled, merge authority follows the live GitHub owner and rulesets.

## Development workflow

Use short-lived branches created from `dev`, then open a pull request back into `dev` for review. Production and stable releases are promoted through a pull request from `dev` into `main`.

Suggested branch prefixes are `feat/`, `fix/`, `refactor/`, `chore/`, `docs/`, and `test/`.

## Branches

- `main` — production and stable release branch.
- `dev` — integration branch for active development.

Direct commits to `main` and `dev` are not part of the normal workflow. The repository owner is the final merge authority for protected branches.

## Contributions

Keep changes focused, use Conventional Commit-style prefixes where practical, explain how changes were tested, update documentation when needed, and never commit secrets or sensitive local configuration. Contributors should request owner review before merging protected-branch changes.

The application stack has not yet been initialized, so installation and runtime instructions will be added later.

## Governance note

GitHub rulesets require the repository owner as the code owner for protected-branch changes and allow the owner to bypass review requirements on the owner's own pull requests. GitHub does not provide a repository-level setting that independently restricts the final merge button to one collaborator when that collaborator already has write access; owner review and the protected-branch ruleset are therefore the enforceable controls here.
