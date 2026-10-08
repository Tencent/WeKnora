-- Mirrors versioned migration 000102_mcp_endpoints:
-- workspace-published MCP server endpoints with per-endpoint token, KB scope
-- and tool allowlist.

CREATE TABLE IF NOT EXISTS mcp_endpoints (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    token_hash VARCHAR(64) NOT NULL DEFAULT '',
    token_hint VARCHAR(16) NOT NULL DEFAULT '',
    knowledge_base_ids TEXT NOT NULL DEFAULT '[]',
    tools TEXT NOT NULL DEFAULT '[]',
    default_agent_id VARCHAR(36) NOT NULL DEFAULT '',
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 60,
    last_used_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_mcp_endpoints_tenant ON mcp_endpoints (tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_endpoints_token_hash
    ON mcp_endpoints (token_hash)
    WHERE token_hash != '' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mcp_endpoints_deleted ON mcp_endpoints (deleted_at) WHERE deleted_at IS NOT NULL;
