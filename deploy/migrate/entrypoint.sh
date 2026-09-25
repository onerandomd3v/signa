#!/bin/sh
set -eu

if [ -z "${SIGNA_DATABASE_URL:-}" ]; then
    echo "SIGNA_DATABASE_URL is required" >&2
    exit 1
fi

exec /app/goose -dir /app/migrations postgres "$SIGNA_DATABASE_URL" up
