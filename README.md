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

## Frontend development

The Next.js application lives in [`apps/web`](apps/web). It requires Node.js 22.22.2 or newer in the supported 22.x line, Node.js 24.15 or newer in the 24.x line, or Node.js 26 or newer, plus npm.

From a fresh clone, install its dependencies and start the development server:

```sh
cd apps/web
npm ci
npm run dev
```

Open <http://localhost:3000>. From `apps/web`, run `npm run lint`, `npm run typecheck`, `npm test`, `npm run format:check`, and `npm run build` for the frontend checks and production build.

Validate and regenerate the TypeScript API client from the canonical OpenAPI contract:

```sh
cd apps/web
npm run api:lint
npm run api:generate
```

The generated client and types are committed under `apps/web/lib/api/generated/`. Run `npm run api:check` to lint the contract, regenerate the output, and verify that generation produces no diff.

Local frontend settings belong in `apps/web/.env.local`, which is ignored by the root `.gitignore`; never commit local environment files or secrets. Copy `apps/web/.env.example` to `apps/web/.env.local` for local development. `NEXT_PUBLIC_API_BASE_URL` is the public Go API origin (defaults to `http://localhost:8080` in development); it is included in browser code and must never contain secrets.

## Go foundation

Requires Go 1.25 or later.

Run the API:

```text
go run ./cmd/api
```

The API listens on `:8080` by default. Check its health with `GET http://localhost:8080/healthz`.

Text-first reports are accepted with `POST /reports` using an `Idempotency-Key` header and a JSON body containing non-empty `raw_text`. An optional `device_location` may include latitude, longitude, and non-negative accuracy. The API acknowledges accepted reports before downstream processing; exact device coordinates are not returned.

Optional local configuration can be copied from `.env.example`:

- `SIGNA_API_ADDR` sets the API listen address.
- `SIGNA_DATABASE_URL` points to the local PostgreSQL/PostGIS service.
- `SIGNA_REDIS_ADDR` points to the local Redis service.
- `SIGNA_SHUTDOWN_TIMEOUT` and `SIGNA_WORKER_INTERVAL` use Go duration values; the worker polls every `500ms` by default.
- `SIGNA_REPORT_RATE_PER_MINUTE` / `SIGNA_REPORT_RATE_BURST` configure per-client report limits; global limits use `SIGNA_REPORT_GLOBAL_RATE_PER_MINUTE` / `SIGNA_REPORT_GLOBAL_RATE_BURST`.
- `SIGNA_WEB_ALLOWED_ORIGINS` configures the comma-separated exact HTTP/HTTPS origins allowed to call the API from a browser; it defaults to `http://localhost:3000`. Wildcards, paths, credentials, and malformed origins are rejected.
- `SIGNA_OBJECT_STORAGE_ENDPOINT`, `SIGNA_OBJECT_STORAGE_REGION`, `SIGNA_OBJECT_STORAGE_BUCKET`, `SIGNA_OBJECT_STORAGE_ACCESS_KEY_ID`, and `SIGNA_OBJECT_STORAGE_SECRET_ACCESS_KEY` configure optional private R2/S3-compatible direct media uploads. Leave them unset to keep text-only reporting available while media uploads return a controlled unavailable response.
- The incident worker requires `SIGNA_INCIDENT_CANDIDATE_RADIUS_METERS`, `SIGNA_INCIDENT_CANDIDATE_TIME_WINDOW`, `SIGNA_INCIDENT_CANDIDATE_LIMIT`, `SIGNA_INCIDENT_SIMILARITY_THRESHOLD`, and `SIGNA_INCIDENT_SIMILARITY_WINNER_MARGIN`; these have no product-policy defaults and are not required by the API process.

Run the worker in another terminal:

```text
go run ./cmd/worker
```

Run tests and static checks:

```text
go test ./...
go vet ./...
```

Pull requests targeting `dev` or `main` enforce the Go quality checks and the web/OpenAPI checks through GitHub Actions.

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

Run the isolated migration and report-ingestion integration tests with:

```text
go test -tags=integration ./...
```

These tests create and remove uniquely named temporary databases. They use `SIGNA_TEST_DATABASE_URL` when set, otherwise the local Compose connection, and never run rollback checks against the application database itself.
