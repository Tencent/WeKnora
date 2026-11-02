#!/bin/bash
set -euo pipefail
version=${SSH_ORIGINAL_COMMAND:-}
[[ "$version" =~ ^[0-9a-f]{40}$ ]] || exit 2
cd /opt/weknora
exec 9>/opt/weknora/deploy.lock
flock -n 9 || exit 3
cat > release.next.yaml <<EOF
services:
  app:
    image: ghcr.io/demoasm/weknora-app:$version
  frontend:
    image: ghcr.io/demoasm/weknora-ui:$version
  docreader:
    image: ghcr.io/demoasm/weknora-docreader:$version
EOF
base=(docker compose -f docker-compose.yml -f compose.cloud.yaml)
"${base[@]}" -f release.next.yaml pull app frontend docreader
mkdir -p backups
# A failed database backup must stop deployment.
"${base[@]}" exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' | gzip > "backups/pre-$version.sql.gz"
if test -f release.yaml; then cp release.yaml release.previous.yaml; fi
rollback() {
  trap - ERR
  if test -f release.previous.yaml; then
    cp release.previous.yaml release.yaml
    "${base[@]}" -f release.yaml up -d --no-build || true
  fi
}
trap rollback ERR
cp release.next.yaml release.yaml
"${base[@]}" -f release.yaml up -d --no-build
for attempt in $(seq 1 60); do
  if curl -fsS --max-time 5 http://127.0.0.1:8080/health && curl -fsS --max-time 5 http://127.0.0.1/ >/dev/null; then
    echo "Deployment healthy: $version"; exit 0
  fi
  sleep 5
done
if test -f release.previous.yaml; then
  cp release.previous.yaml release.yaml
  "${base[@]}" -f release.yaml up -d --no-build
fi
echo 'Deployment failed; inspect database migration compatibility before retrying.' >&2
exit 1