#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target="${1:-sandbox}"
profile="${3:-office-core}"
case "$target" in sandbox|cube) ;; *) echo 'Usage: build_sandbox_office.sh [sandbox|cube] [tag] [office-core]' >&2; exit 2 ;; esac
case "$profile" in office-core) ;; *) echo "Unknown profile: $profile" >&2; exit 2 ;; esac
version="$(cd "$root" && go run ./cmd/builtin-skills-manifest -version)"
default_tag="weknora-sandbox:$profile-$version"
if [[ "$target" == cube ]]; then default_tag="${default_tag}-cube"; fi
tag="${2:-$default_tag}"
if [[ "$target" == cube ]]; then export DOCKER_DEFAULT_PLATFORM=linux/amd64; fi
build_target="$profile"
if [[ "$target" == cube ]]; then
    build_target="$profile-cube"
fi
manifest="$(cd "$root" && go run ./cmd/builtin-skills-manifest -profile "$profile")"
docker build -f "$root/docker/Dockerfile.sandbox" --target runtime -t weknora-sandbox:base "$root"
docker build --build-arg "CORE_SKILLS_MANIFEST=$manifest" --build-arg "BUILTIN_SKILLS_VERSION=$version" \
    -f "$root/docker/Dockerfile.sandbox-office" --target "$build_target" -t "$tag" "$root"

manifest_dir="$root/dist/skills"
mkdir -p "$manifest_dir"
manifest_file="$manifest_dir/${tag//[\/:]/_}.skills.json"
printf '%s\n' "$manifest" > "$manifest_file"
printf 'Skill manifest: %s\n' "$manifest_file"
