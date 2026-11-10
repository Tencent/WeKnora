#!/usr/bin/env bash
# check-skill-wire-vocab.sh — fail if the agent-facing docs still reference wire
# vocabulary that the CLI has renamed or removed. Wire-shape changes must sweep
# every agent-facing doc in the same PR.
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
  "kb init:kb config set (renamed — kb init removed)"
  "continue-stream:session resume (renamed)"
  "retry_command:retry_argv (error envelope field renamed in v0.10)"
)

# Current-truth sources only. AGENTS.md is the authoritative wire contract that
# skills are condensed from — a rename that lands in skills/ but not here leaves
# the source of truth wrong.
#
# CHANGELOG.md and README.md are deliberately NOT scanned. Both will name
# legacy terms when recording a rename or writing upgrade notes for agents
# moving between CLI versions; scanning them would guarantee a false positive.
SCAN_TARGETS=(skills/ AGENTS.md)

# The v0.10 retry_command miss happened because the scan silently covered less
# than the docs that needed covering. Fail loudly if a target disappears rather
# than quietly shrinking coverage again.
for target in "${SCAN_TARGETS[@]}"; do
  if [ ! -e "$target" ]; then
    echo "scan target '$target' does not exist — update SCAN_TARGETS" >&2
    exit 2
  fi
done

fail=0
for entry in "${BANNED[@]}"; do
  term="${entry%%:*}"
  why="${entry#*:}"
  if hits=$(grep -rn --include='*.md' -F "$term" "${SCAN_TARGETS[@]}" 2>/dev/null); then
    echo "BANNED wire vocab '$term' found (use: $why):"
    echo "$hits"
    fail=1
  fi
done
exit $fail
