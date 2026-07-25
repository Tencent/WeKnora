#!/usr/bin/env bash

# Run the MySQL migration chain against a brand-new MySQL 8 instance.
#
# This script deliberately does not use the developer's normal MySQL volume:
# every invocation gets an ephemeral container and removes it on exit. It is
# therefore safe to use while iterating on dialect-specific migrations.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MIGRATIONS_DIR="$REPO_ROOT/migrations/mysql"

test_container="weknora-migration-test-$RANDOM"
test_password="weknora-migration-test"
test_database="weknora_migration_test"

cleanup() {
    docker rm -f "$test_container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run --rm -d \
    --name "$test_container" \
    -e "MYSQL_ROOT_PASSWORD=$test_password" \
    -e "MYSQL_DATABASE=$test_database" \
    mysql:8.0 \
    --character-set-server=utf8mb4 \
    --collation-server=utf8mb4_unicode_ci >/dev/null

for _ in $(seq 1 30); do
    if docker exec -e "MYSQL_PWD=$test_password" "$test_container" mysqladmin ping -h 127.0.0.1 -uroot --silent >/dev/null 2>&1; then
        break
    fi
    sleep 2
done

if ! docker exec -e "MYSQL_PWD=$test_password" "$test_container" mysqladmin ping -h 127.0.0.1 -uroot --silent >/dev/null 2>&1; then
    echo "MySQL did not become ready" >&2
    exit 1
fi

for migration in $(find "$MIGRATIONS_DIR" -maxdepth 1 -name '*.up.sql' -print | LC_ALL=C sort); do
    echo "Applying $migration"
    docker exec -e "MYSQL_PWD=$test_password" -i "$test_container" mysql -uroot "$test_database" < "$migration"
done

field=$(docker exec -e "MYSQL_PWD=$test_password" "$test_container" mysql -N -B -uroot "$test_database" \
    -e "SHOW COLUMNS FROM knowledges LIKE 'pending_subtasks_count';" | awk '{print $1}')

if [ "$field" != "pending_subtasks_count" ]; then
    echo "pending_subtasks_count was not created by the MySQL migration chain" >&2
    exit 1
fi

echo "MySQL migration chain passed"
