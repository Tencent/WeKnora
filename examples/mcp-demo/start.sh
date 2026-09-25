#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

export MCP_SERVER_AUTH_TOKEN="${MCP_SERVER_AUTH_TOKEN:-weknora-demo-token}"
export MCP_HOST="${MCP_HOST:-127.0.0.1}"
export MCP_PORT="${MCP_PORT:-8010}"
export MCP_TRANSPORT="${MCP_TRANSPORT:-http}"

VENV_DIR=".venv"
if [[ ! -d "$VENV_DIR" ]]; then
  echo "Creating virtualenv in $VENV_DIR ..."
  python3 -m venv "$VENV_DIR"
fi
# shellcheck disable=SC1091
source "$VENV_DIR/bin/activate"

if ! python -c "import mcp" 2>/dev/null; then
  echo "Installing dependencies..."
  pip install -r requirements.txt
fi

echo "MCP Demo"
echo "  transport : ${MCP_TRANSPORT}"
echo "  endpoint  : http://${MCP_HOST}:${MCP_PORT}/$([ "$MCP_TRANSPORT" = http ] && echo mcp || echo sse)"
echo "  auth token: ${MCP_SERVER_AUTH_TOKEN}"
echo
echo "WeKnora UI → 设置 → MCP 服务 → 新建"
echo "  传输: HTTP Streamable"
echo "  URL : http://${MCP_HOST}:${MCP_PORT}/mcp"
echo "  认证: Bearer / ${MCP_SERVER_AUTH_TOKEN}"
echo

exec python server.py --transport "$MCP_TRANSPORT" --host "$MCP_HOST" --port "$MCP_PORT"
