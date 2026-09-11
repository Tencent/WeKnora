#!/bin/bash
set -eu

# Exercise URL construction without loading .env or contacting a database.
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
run_case() (
    DB_HOST=localhost DB_PORT=5432 DB_USER=test DB_PASSWORD=test DB_NAME=test
    DB_URL="$1" DB_SSLMODE="${2:-disable}"
    source <(sed -n '/^# Construct the database URL/,/^# Execute migration/{ /^# Execute migration/!p; }' "$SCRIPT_DIR/migrate.sh")
    printf '%s' "$DB_URL"
)

for mode in disable require verify-ca verify-full; do
    result=$(run_case "" "$mode")
    [[ "$result" == "postgres://test:test@localhost:5432/test?sslmode=$mode" ]]
    result=$(run_case 'postgres://test@localhost/test?connect_timeout=5' "$mode")
    [[ "$result" == "postgres://test@localhost/test?connect_timeout=5&sslmode=$mode" ]]
    result=$(run_case "postgres://test@localhost/test?sslmode=$mode&connect_timeout=5" invalid)
    [[ "$result" == "postgres://test@localhost/test?sslmode=$mode&connect_timeout=5" ]]
done
[[ "$(run_case '' '')" == 'postgres://test:test@localhost:5432/test?sslmode=disable' ]]
[[ "$(run_case 'postgres://test@localhost/test' require)" == 'postgres://test@localhost/test?sslmode=require' ]]
for mode in allow prefer nossl ''; do
    if run_case "postgres://test@localhost/test?sslmode=$mode" disable >/dev/null 2>&1; then
        echo "Unsupported URL mode accepted: $mode" >&2
        exit 1
    fi
done
for mode in allow prefer nossl 'require&sslmode=disable'; do
    if run_case '' "$mode" >/dev/null 2>&1; then
        echo "Unsupported environment mode accepted: $mode" >&2
        exit 1
    fi
done
echo 'PostgreSQL migration SSL mode tests passed'
