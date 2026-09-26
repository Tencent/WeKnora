#!/usr/bin/env bash
# Packages the activity example plugin as a .wkp: plugin.yaml, schemas,
# main.py, the page under ui/ with the bridge library, and the Python SDK
# under vendor/.
set -euo pipefail
cd "$(dirname "$0")"
version=$(sed -n 's/^version: //p' plugin.yaml)
out=${1:-"weknora-examples-activity-${version}.wkp"}
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cp plugin.yaml main.py icon.svg "$stage"/
cp -r ui schemas "$stage"/
cp ../../../packages/plugin-ui/index.js "$stage/ui/weknora-plugin-ui.js"
cp ../../../packages/plugin-ui/kit.css "$stage/ui/weknora-plugin-ui.css"
mkdir -p "$stage/vendor"
cp -r ../../../pluginsdk/python/src/weknora_plugin "$stage/vendor/"
find "$stage" -name __pycache__ -prune -exec rm -rf {} +
rm -f "$out"
(cd "$stage" && zip -qr - .) > "$out"
echo "wrote $out"
