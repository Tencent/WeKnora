#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT

required_files=(
  "internal/custom/handler/processing.go"
  "internal/custom/migrations/000008_processing_observability.up.sql"
  "internal/custom/migrations/000008_processing_observability.down.sql"
)

for file in "${required_files[@]}"; do
  if [[ ! -s "$file" ]]; then
    echo "custom-backend contract is missing: $file" >&2
    exit 1
  fi
done

echo "Checking custom-backend compilation..."
go build -o "$build_dir/custom-backend" ./cmd/custom-backend

echo "Checking custom migration contract..."
go test ./internal/custom/migrations

echo "custom-backend contract check passed."
