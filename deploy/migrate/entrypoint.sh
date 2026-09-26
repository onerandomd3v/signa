#!/bin/sh
set -eu

if [ -z "${SIGNA_DATABASE_URL:-}" ]; then
    echo "SIGNA_DATABASE_URL is required" >&2
    exit 1
fi

# Goose binary and migrations directory. These match what deploy/migrate/Dockerfile
# installs; they are overridable only so deploy/migrate/entrypoint_test.sh can
# exercise the retry loop with a fake goose and no real database.
goose_bin="${SIGNA_GOOSE_BIN:-/app/goose}"
migrations_dir="${SIGNA_MIGRATIONS_DIR:-/app/migrations}"

# Compose/OpenShip start-ordering does not guarantee Postgres is accepting
# connections before this job runs, so `goose up` can fail with
# "connection refused". `goose up` is idempotent (already-applied migrations
# are skipped), so retry it until it succeeds or the attempt budget is spent.
#
# The default budget (90 attempts x 2s = 180s) intentionally exceeds the
# Postgres readiness window configured in deploy/compose.openship.yaml (~160s:
# a 10s start period plus 30 health-check retries at a 5s interval), so a
# database that becomes healthy within its own window is never abandoned early.
# Operators can widen it for slower databases via the two variables below.
attempt=0
max_attempts="${SIGNA_MIGRATE_MAX_ATTEMPTS:-90}"
retry_delay="${SIGNA_MIGRATE_RETRY_DELAY:-2}"

until "$goose_bin" -dir "$migrations_dir" postgres "$SIGNA_DATABASE_URL" up; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge "$max_attempts" ]; then
        echo "migrations did not succeed after ${attempt} attempts" >&2
        exit 1
    fi
    echo "database not ready yet (attempt ${attempt}/${max_attempts}); retrying in ${retry_delay}s..." >&2
    sleep "$retry_delay"
done
