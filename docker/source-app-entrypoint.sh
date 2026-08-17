#!/bin/bash
set -euo pipefail

# Air writes the rebuilt binary here (Linux filesystem, not the Windows bind mount).
mkdir -p /tmp/weknora-air

cd /app

if [ ! -f go.mod ]; then
    echo "source-app-entrypoint: /app/go.mod missing. Mount the repo with docker-compose.source.yml (.:/app)." >&2
    exit 1
fi

exec "$@"
