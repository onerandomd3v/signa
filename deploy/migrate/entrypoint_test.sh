#!/bin/sh
# Regression coverage for deploy/migrate/entrypoint.sh retry behavior.
#
# A fake `goose` (injected via SIGNA_GOOSE_BIN) records how many times it is
# invoked and can be told to fail the first N calls, so the retry loop is
# exercised with no real database. Run: `sh deploy/migrate/entrypoint_test.sh`.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
entrypoint="$here/entrypoint.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
count_file="$work/count"

# write_fake_goose N  -> fail the first N invocations, then succeed.
# write_fake_goose always -> fail every invocation.
write_fake_goose() {
    printf '0' > "$count_file"
    cat > "$work/goose" <<EOF
#!/bin/sh
n=\$(cat "$count_file")
n=\$((n + 1))
printf '%s' "\$n" > "$count_file"
[ "$1" = "always" ] && exit 1
[ "\$n" -le "$1" ] && exit 1
exit 0
EOF
    chmod +x "$work/goose"
}

attempts() { cat "$count_file"; }
fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

# Inline env prefix keeps each run isolated; retry delay 0 keeps the test fast.
run_entrypoint() { # $1 = max attempts
    SIGNA_DATABASE_URL=postgres://test \
    SIGNA_GOOSE_BIN="$work/goose" \
    SIGNA_MIGRATIONS_DIR="$work" \
    SIGNA_MIGRATE_RETRY_DELAY=0 \
    SIGNA_MIGRATE_MAX_ATTEMPTS="$1" \
    sh "$entrypoint" >/dev/null 2>&1
}

# 1. Immediate success -> exit 0 after a single attempt.
write_fake_goose 0
run_entrypoint 5 || fail "immediate success should exit 0"
[ "$(attempts)" = "1" ] || fail "immediate success should invoke goose once, got $(attempts)"

# 2. Eventual success -> fails twice, succeeds on the third attempt.
write_fake_goose 2
run_entrypoint 5 || fail "eventual success should exit 0"
[ "$(attempts)" = "3" ] || fail "eventual success should invoke goose 3 times, got $(attempts)"

# 3. Budget exhaustion -> exit non-zero after exactly max_attempts.
write_fake_goose always
if run_entrypoint 4; then fail "exhausted budget should exit non-zero"; fi
[ "$(attempts)" = "4" ] || fail "exhaustion should invoke goose max_attempts (4) times, got $(attempts)"

# 4. Missing SIGNA_DATABASE_URL -> exit non-zero without invoking goose.
write_fake_goose 0
if SIGNA_DATABASE_URL= SIGNA_GOOSE_BIN="$work/goose" sh "$entrypoint" >/dev/null 2>&1; then
    fail "missing SIGNA_DATABASE_URL should exit non-zero"
fi
[ "$(attempts)" = "0" ] || fail "missing DB URL should not invoke goose, got $(attempts)"

printf 'All migrate entrypoint retry tests passed.\n'
