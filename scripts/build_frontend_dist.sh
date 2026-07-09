#!/usr/bin/env bash
# Build frontend static assets for Docker / release packaging.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# 仅当 WEKNORA_BASE_PATH 未被 Shell 导出，且根目录存在 .env 时，安全读取该单一配置值
if [ -z "${WEKNORA_BASE_PATH:-}" ] && [ -f "$PROJECT_ROOT/.env" ]; then
	echo "[INFO] Extracting WEKNORA_BASE_PATH from root .env..."
	WEKNORA_BASE_PATH="$(
		awk -F= '
			/^[[:space:]]*WEKNORA_BASE_PATH=/ {
				sub(/^[^=]*=/, "")
				print
			}
		' "$PROJECT_ROOT/.env" \
		| tail -n 1 \
		| sed 's/^[[:space:]]*//; s/[[:space:]]*$//; s/^"//; s/"$//; s/^'\''//; s/'\''$//'
	)"
	export WEKNORA_BASE_PATH
fi

if [ -z "${VITE_FRONTEND_COMMIT:-}" ]; then
	# shellcheck source=/dev/null
	eval "$("$PROJECT_ROOT/scripts/get_version.sh" env)"
	export VITE_FRONTEND_COMMIT="${COMMIT_ID:-unknown}"
fi

export VITE_IS_DOCKER="${VITE_IS_DOCKER:-true}"

cd "$PROJECT_ROOT/frontend"
npm ci
npm run build
