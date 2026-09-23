# Signa

Signa is a real-time community intelligence and safety platform that converts fragmented local reports into continuously updated, confidence-aware incidents and distributes relevant safety information to the people who need it first.

## Status

Signa is in early architecture and MVP development.

The platform addresses the gap between scattered, fast-moving local reports and the timely, trustworthy information people need to make safer decisions. Its architecture is designed to turn reports into relevant signals while preserving confidence and context.

The current technical source of truth is the [System Architecture](docs/architecture.md).

Before implementation, read the repository-wide [Codex instructions](AGENTS.md), the [ADR index](docs/adr/), and the [Signa Linear project](https://linear.app/codeddevs/project/signa-7d2f8fe71827).

## Development workflow

Use short-lived branches created from `dev`, then open a pull request back into `dev` for review. Production and stable releases are promoted through a pull request from `dev` into `main`.

Suggested branch prefixes are `feat/`, `fix/`, `refactor/`, `chore/`, `docs/`, and `test/`.

## Branches

- `main` — production and stable release branch.
- `dev` — integration branch for active development.

Direct commits to `main` and `dev` are not part of the normal workflow. The repository owner, `onerandomd3v`, is the final merge authority for protected-branch pull requests.

## Contributions

Keep changes focused, use Conventional Commit-style prefixes where practical, explain how changes were tested, update documentation when needed, and never commit secrets or sensitive local configuration. Collaborators must request owner review and must not merge protected-branch PRs. The owner merges after required checks pass; for owner-authored PRs, the owner may use the PR-only bypass without another person's approval, even if GitHub still displays “Review required.”

## Go foundation

Requires Go 1.25 or later.

Run the API:

```text
go run ./cmd/api
```

The API listens on `:8080` by default. Check its health with `GET http://localhost:8080/healthz`.

Optional local configuration can be copied from `.env.example`:

- `SIGNA_API_ADDR` sets the API listen address.
- `SIGNA_DATABASE_URL` points to the local PostgreSQL/PostGIS service.
- `SIGNA_REDIS_ADDR` points to the local Redis service.
- `SIGNA_SHUTDOWN_TIMEOUT` and `SIGNA_WORKER_INTERVAL` use Go duration values such as `10s` or `500ms`.

Run the worker in another terminal:

```text
go run ./cmd/worker
```

Run tests and static checks:

```text
go test ./...
go vet ./...
```

## Local data infrastructure

Start and stop the local PostgreSQL/PostGIS and Redis services with:

```text
make infra-up
make infra-down
```

Apply the PostGIS baseline migration and verify connectivity with:

```text
make migrate-up
make db-ping
make redis-ping
```

The Compose file uses local-only credentials (`signa` / `signa_local`). Do not reuse them outside local development. The integration checks are explicit and are not included in the normal `go test ./...` run.
