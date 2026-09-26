#!/usr/bin/env bash
# Packages the notebooks example plugin as a .wkp: plugin.yaml, schemas,
# main.py and the Python SDK vendored under vendor/. Nothing is compiled;
# WeKnora runs main.py with its own python3.
set -euo pipefail
cd "$(dirname "$0")"
version=$(sed -n 's/^version: //p' plugin.yaml)
out=${1:-"weknora-examples-notebooks-${version}.wkp"}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cp plugin.yaml main.py "$stage"/
cp -r schemas "$stage"/
mkdir -p "$stage/vendor"
cp -r ../../../pluginsdk/python/src/weknora_plugin "$stage/vendor/"
find "$stage" -name __pycache__ -prune -exec rm -rf {} +
rm -f "$out"
(cd "$stage" && zip -qr - .) > "$out"
echo "wrote $out"
