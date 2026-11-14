-- Per-tool MCP risk. risk is the workspace's explicit setting; observed_risk
-- is the highest risk the service has declared through tool annotations.
-- Empty values mean "not set" so existing rows keep their behaviour.
ALTER TABLE mcp_tool_approvals
    ADD COLUMN IF NOT EXISTS risk VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE mcp_tool_approvals
    ADD COLUMN IF NOT EXISTS observed_risk VARCHAR(16) NOT NULL DEFAULT '';
