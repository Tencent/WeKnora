-- Per-tool MCP enable/disable policy. Missing rows are treated as enabled.
ALTER TABLE mcp_tool_approvals ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT 1;
