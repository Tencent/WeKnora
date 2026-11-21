CREATE TABLE IF NOT EXISTS mcp_tool_session_policies (
    tenant_id BIGINT NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    tool_name VARCHAR(512) NOT NULL,
    allowed_sessions JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, service_id, tool_name)
);
