-- Migration: 000102_mcp_endpoints
-- Workspace-published MCP server endpoints: each row is one bearer-token
-- protected MCP surface with its own knowledge-base scope and tool allowlist.
DO $$ BEGIN RAISE NOTICE '[Migration 000102] Creating mcp_endpoints table'; END $$;

CREATE TABLE IF NOT EXISTS mcp_endpoints (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    token_hash VARCHAR(64) NOT NULL DEFAULT '',
    token_hint VARCHAR(16) NOT NULL DEFAULT '',
    knowledge_base_ids JSONB NOT NULL DEFAULT '[]',
    tools JSONB NOT NULL DEFAULT '[]',
    default_agent_id VARCHAR(36) NOT NULL DEFAULT '',
    rate_limit_per_minute INTEGER NOT NULL DEFAULT 60,
    last_used_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_mcp_endpoints_tenant ON mcp_endpoints (tenant_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_endpoints_token_hash
    ON mcp_endpoints (token_hash)
    WHERE token_hash <> '' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mcp_endpoints_deleted ON mcp_endpoints (deleted_at) WHERE deleted_at IS NOT NULL;

COMMENT ON TABLE mcp_endpoints IS 'Workspace-published MCP server endpoints consumed by external MCP clients';
COMMENT ON COLUMN mcp_endpoints.token_hash IS 'SHA-256 hex of the bearer token (mcp_ prefix); plaintext is shown once and never stored';
COMMENT ON COLUMN mcp_endpoints.token_hint IS 'Leading characters of the token for display only';
COMMENT ON COLUMN mcp_endpoints.knowledge_base_ids IS 'Knowledge-base allowlist; empty array means every workspace knowledge base';
COMMENT ON COLUMN mcp_endpoints.tools IS 'Allowlisted MCP tool names from the endpoint tool catalog';
COMMENT ON COLUMN mcp_endpoints.default_agent_id IS 'Agent used by the ask tool when the caller does not name one';
