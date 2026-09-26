#!/bin/sh
set -eu

if [ -z "${SIGNA_DATABASE_URL:-}" ]; then
    echo "SIGNA_DATABASE_URL is required" >&2
    exit 1
fi

# Compose/OpenShip start-ordering does not guarantee Postgres is accepting
# connections before this job runs, so `goose up` can fail with
# "connection refused". `goose up` is idempotent (already-applied migrations
# are skipped), so retry it until it succeeds or the attempt budget is spent.
attempt=0
max_attempts="${SIGNA_MIGRATE_MAX_ATTEMPTS:-30}"
retry_delay="${SIGNA_MIGRATE_RETRY_DELAY:-2}"

until /app/goose -dir /app/migrations postgres "$SIGNA_DATABASE_URL" up; do
    attempt=$((attempt + 1))
    if [ "$attempt" -ge "$max_attempts" ]; then
        echo "migrations did not succeed after ${attempt} attempts" >&2
        exit 1
    fi
    echo "database not ready yet (attempt ${attempt}/${max_attempts}); retrying in ${retry_delay}s..." >&2
    sleep "$retry_delay"
done
