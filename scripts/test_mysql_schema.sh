#!/usr/bin/env sh

set -eu

IMAGE="${MYSQL_IMAGE:-mysql:8.4}"
CONTAINER="weknora-mysql-schema-test-$$"
DATABASE="weknora_schema_test"
ROOT_PASSWORD="weknora_schema_test_root"
SCHEMA="migrations/mysql/000074_baseline.up.sql"

cleanup() {
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if ! docker info >/dev/null 2>&1; then
  echo "Docker is required to run the MySQL schema test." >&2
  exit 1
fi

if [ ! -f "$SCHEMA" ]; then
  echo "MySQL schema migration not found: $SCHEMA" >&2
  exit 1
fi

docker run --name "$CONTAINER" -d \
  -e "MYSQL_ROOT_PASSWORD=$ROOT_PASSWORD" \
  -e "MYSQL_DATABASE=$DATABASE" \
  "$IMAGE" \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_0900_ai_ci >/dev/null

attempt=0
until docker exec "$CONTAINER" mysql -h 127.0.0.1 --protocol=TCP -uroot "-p$ROOT_PASSWORD" "$DATABASE" -e "SELECT 1" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 60 ]; then
    docker logs "$CONTAINER" >&2 || true
    echo "MySQL did not become ready within 60 seconds." >&2
    exit 1
  fi
  sleep 1
done

docker cp "$SCHEMA" "$CONTAINER:/tmp/schema.sql"
docker exec "$CONTAINER" sh -c "mysql -h 127.0.0.1 --protocol=TCP -uroot -p$ROOT_PASSWORD $DATABASE < /tmp/schema.sql"

table_count="$(docker exec "$CONTAINER" mysql -h 127.0.0.1 --protocol=TCP -N -uroot "-p$ROOT_PASSWORD" "$DATABASE" -e \
  "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE';")"
if [ "$table_count" -ne 50 ]; then
  echo "Expected 50 tables, got $table_count." >&2
  exit 1
fi

docker exec "$CONTAINER" mysql -h 127.0.0.1 --protocol=TCP -uroot "-p$ROOT_PASSWORD" "$DATABASE" -e \
  "INSERT INTO vector_stores (id, name, engine_type, tenant_id) VALUES ('store-1', 'primary', 'qdrant', 1); \
   UPDATE vector_stores SET deleted_at = CURRENT_TIMESTAMP(3) WHERE id = 'store-1'; \
   INSERT INTO vector_stores (id, name, engine_type, tenant_id) VALUES ('store-2', 'primary', 'qdrant', 1); \
   INSERT INTO tenant_invitations (tenant_id, invitee_user_id, role, expires_at) VALUES (1, 'user-1', 'viewer', DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL 1 DAY)); \
   SELECT JSON_UNQUOTE(JSON_EXTRACT(JSON_OBJECT('model_id', 'model-1'), '$.model_id')); \
   SELECT JSON_CONTAINS(JSON_ARRAY('knowledge-1'), JSON_ARRAY('knowledge-1'));" >/dev/null

if docker exec "$CONTAINER" mysql -h 127.0.0.1 --protocol=TCP -uroot "-p$ROOT_PASSWORD" "$DATABASE" -e \
  "INSERT INTO tenant_invitations (tenant_id, invitee_user_id, role, expires_at) VALUES (1, 'user-1', 'viewer', DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL 1 DAY));" >/dev/null 2>&1; then
  echo "Expected duplicate active invitation to be rejected." >&2
  exit 1
fi

echo "MySQL schema validation passed ($table_count tables)."
