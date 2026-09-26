-- MCP services plugins provide are not rows of mcp_services, so their tool
-- policies could not be stored. SQLite cannot drop a foreign key, so the
-- table is rebuilt without it (versioned 000123).
CREATE TABLE mcp_tool_approvals_next (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL,
    tool_name VARCHAR(512) NOT NULL,
    require_approval BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    enabled BOOLEAN NOT NULL DEFAULT 1
);
INSERT INTO mcp_tool_approvals_next
    (id, tenant_id, service_id, tool_name, require_approval, created_at, updated_at, enabled)
SELECT id, tenant_id, service_id, tool_name, require_approval, created_at, updated_at, enabled
FROM mcp_tool_approvals;
DROP TABLE mcp_tool_approvals;
ALTER TABLE mcp_tool_approvals_next RENAME TO mcp_tool_approvals;
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_tool_approvals_tenant_svc_tool
    ON mcp_tool_approvals(tenant_id, service_id, tool_name);
CREATE INDEX IF NOT EXISTS idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);
