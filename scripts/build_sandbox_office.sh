#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target="${1:-sandbox}"
case "$target" in sandbox|cube) ;; *) echo 'Usage: build_sandbox_office.sh [sandbox|cube] [tag]' >&2; exit 2 ;; esac
default_tag=weknora-sandbox:office-browser-2026.09.2
if [[ "$target" == cube ]]; then default_tag="${default_tag}-cube"; fi
tag="${2:-$default_tag}"
if [[ "$target" == cube ]]; then export DOCKER_DEFAULT_PLATFORM=linux/amd64; fi
docker build -f "$root/docker/Dockerfile.sandbox" --target runtime -t weknora-sandbox:base "$root"
manifest="$(cd "$root" && go run ./cmd/builtin-skills-manifest)"
docker build --build-arg "BUILTIN_SKILLS_MANIFEST=$manifest" -f "$root/docker/Dockerfile.sandbox-office" --target "$target" -t "$tag" "$root"

manifest_dir="$root/dist/skills"
mkdir -p "$manifest_dir"
manifest_file="$manifest_dir/${tag//[\/:]/_}.skills.json"
printf '%s\n' "$manifest" > "$manifest_file"
printf 'Skill manifest: %s\n' "$manifest_file"
