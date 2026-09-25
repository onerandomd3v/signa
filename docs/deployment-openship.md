# OpenShip deployment

Signa deploys the Go API and worker as separate OpenShip services from the
repository root. The existing `deploy/docker-compose.yml` remains the local
PostgreSQL/PostGIS and Redis setup and is not a production deployment
definition.

## Images and process model

Build with the repository root as the Docker context:

```text
docker build -f deploy/api/Dockerfile -t signa-api:<git-sha> .
docker build -f deploy/worker/Dockerfile -t signa-worker:<git-sha> .
docker build -f deploy/migrate/Dockerfile -t signa-migrate:<git-sha> .
```

The API image starts only `/app/signa-api`, uses `/app` as its working
directory, includes `contracts/`, and listens on container port `8080`.
OpenShip should route its public service to that port. Its health check is
`GET /healthz`.

The worker image starts only `/app/signa-worker`, uses `/app` as its working
directory, includes `contracts/` for the AI and similarity schemas, and
exposes no public port.

Neither application image contains credentials or environment-specific
values. Configure secrets and runtime settings in OpenShip. In particular,
do not use `localhost` for shared services: `SIGNA_DATABASE_URL` must use the
internal PostgreSQL hostname and port supplied by the OpenShip PostgreSQL
service, and `SIGNA_REDIS_ADDR` must use the internal Redis hostname and port
supplied by the OpenShip Redis service (normally `:5432` and `:6379`).

## Migrations

Run the migration image as one OpenShip release/job command before starting or
rolling the API and worker services:

```text
-dir /app/migrations postgres "$SIGNA_DATABASE_URL" up
```

The migration image's entrypoint is `/app/goose`, so the release/job command
above is passed as its arguments. The job receives `SIGNA_DATABASE_URL` at
runtime from OpenShip. It uses the repository's existing Goose version,
`v3.27.0`, and exits after applying pending migrations. Wait for this job to
succeed before deploying or restarting the API and worker. Do not add
migration commands to either application service; this prevents the two
services from racing during startup.

The migration image is built from `deploy/migrate/Dockerfile` with the same
repository-root context as the application images. It contains only the
compiled Goose binary and `migrations/`; no database URL or other secret is
baked into it.

## Required runtime configuration

Use the existing deployment environment examples as the key inventory:

- API: `deploy/api/.env.example`
- Worker: `deploy/worker/.env.example`

Keep product-policy values supplied by the approved deployment configuration;
this deployment definition does not invent defaults for them.
