-- MCP tool human-approval flags (per service + tool name, issue #1173)
DO $$ BEGIN RAISE NOTICE '[Migration 000042] Creating mcp_tool_approvals...'; END $$;

CREATE TABLE IF NOT EXISTS mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    tool_name VARCHAR(512) NOT NULL,
    require_approval BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_tool_approvals_tenant_svc_tool
    ON mcp_tool_approvals(tenant_id, service_id, tool_name);

CREATE INDEX IF NOT EXISTS idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000042] mcp_tool_approvals ready'; END $$;
