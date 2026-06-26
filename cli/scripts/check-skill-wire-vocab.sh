#!/usr/bin/env bash
# check-skill-wire-vocab.sh — fail if cli/skills/ still references wire
# vocabulary that the CLI has renamed or removed. Wire-shape changes must
# sweep skills in the same PR (pattern borrowed from larksuite/cli).
set -euo pipefail
cd "$(dirname "$0")/.."

# legacy_term:replacement — extend this list in the SAME commit that
# renames/removes a flag, command, tool, or error code.
BANNED=(
  "agent_invoke:session_ask (MCP tool renamed in v0.9)"
  "agent invoke:session ask --agent (moved in v0.9)"
  "mcp.readonly_mode:removed in v0.9 (never emitted)"
  "mcp.tool_not_allowed:removed in v0.9 (never emitted)"
  "mcp.schema_unknown_command:removed in v0.9 (never emitted)"
  "auth login --host:profile add --host (auth login dropped --host in v0.9)"
  "auth login --name:profile add <name> (auth login dropped --name in v0.9)"
  "agent create --kb:agent create --attach-kb (renamed in v0.9)"
)

fail=0
for entry in "${BANNED[@]}"; do
  term="${entry%%:*}"
  why="${entry#*:}"
  if hits=$(grep -rn --include='*.md' -F "$term" skills/ 2>/dev/null); then
    echo "BANNED wire vocab '$term' found (use: $why):"
    echo "$hits"
    fail=1
  fi
done
exit $fail
