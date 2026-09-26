#!/usr/bin/env bash
# Builds the subtitles example plugin for the platforms WeKnora runs on and zips it
# into a .wkp package: plugin.yaml and bin/<os>-<arch>/subtitles.
set -euo pipefail
cd "$(dirname "$0")"
version=$(sed -n 's/^version: //p' plugin.yaml)
out=${1:-"weknora-examples-subtitles-${version}.wkp"}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cp plugin.yaml "$stage"/
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os=${target%/*}
  arch=${target#*/}
  bin="$stage/bin/${os}-${arch}/subtitles"
  [ "$os" = windows ] && bin="$bin.exe"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w" -o "$bin" .
done
rm -f "$out"
(cd "$stage" && zip -qr - .) > "$out"
echo "wrote $out"
