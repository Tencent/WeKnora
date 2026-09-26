#!/usr/bin/env bash
# Builds the RSS example plugin for the platforms WeKnora runs on and zips it
# into a .wkp package: plugin.yaml, schemas, icon and bin/<os>-<arch>/rss.
set -euo pipefail
cd "$(dirname "$0")"
version=$(sed -n 's/^version: //p' plugin.yaml)
out=${1:-"weknora-examples-rss-${version}.wkp"}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cp plugin.yaml icon.svg "$stage"/
cp -r schemas "$stage"/
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os=${target%/*}
  arch=${target#*/}
  bin="$stage/bin/${os}-${arch}/rss"
  [ "$os" = windows ] && bin="$bin.exe"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w" -o "$bin" .
done
rm -f "$out"
(cd "$stage" && zip -qr - .) > "$out"
echo "wrote $out"
