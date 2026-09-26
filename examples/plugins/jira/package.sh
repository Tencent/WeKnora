#!/usr/bin/env bash
# Builds the Jira example plugin for the platforms WeKnora runs on and zips it
# into a .wkp package: plugin.yaml, schemas, the skill, icon and bin/<os>-<arch>/jira.
set -euo pipefail
cd "$(dirname "$0")"
version=$(sed -n 's/^version: //p' plugin.yaml)
out=${1:-"weknora-examples-jira-${version}.wkp"}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cp plugin.yaml icon.svg "$stage"/
cp -r schemas skills "$stage"/
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os=${target%/*}
  arch=${target#*/}
  bin="$stage/bin/${os}-${arch}/jira"
  [ "$os" = windows ] && bin="$bin.exe"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w" -o "$bin" .
done
rm -f "$out"
(cd "$stage" && zip -qr - .) > "$out"
echo "wrote $out"
