-- MySQL 8 translation of 000042_mcp_tool_approval.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

CREATE TABLE mcp_tool_approvals (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    tool_name VARCHAR(512) NOT NULL,
    require_approval TINYINT(1) NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_mcp_tool_approvals_service_id ON mcp_tool_approvals(service_id);
