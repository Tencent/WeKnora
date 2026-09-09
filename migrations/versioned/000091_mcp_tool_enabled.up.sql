-- Per-tool MCP enable/disable policy. Existing rows and missing rows remain
-- enabled by default so this migration is backwards compatible.
ALTER TABLE mcp_tool_approvals
    ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT true;
