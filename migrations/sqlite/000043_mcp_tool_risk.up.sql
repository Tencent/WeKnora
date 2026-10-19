-- Per-tool MCP risk: the workspace's explicit setting and the highest risk
-- declared through tool annotations. Empty means "not set".
ALTER TABLE mcp_tool_approvals ADD COLUMN risk VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE mcp_tool_approvals ADD COLUMN observed_risk VARCHAR(16) NOT NULL DEFAULT '';
