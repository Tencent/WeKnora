-- Migration: 000137_mcp_personal_endpoints
-- Personal MCP endpoints: any member may publish an endpoint that acts as
-- themselves. Existing rows stay space endpoints so their tokens keep working.
DO $$ BEGIN RAISE NOTICE '[Migration 000137] Adding personal MCP endpoints'; END $$;

ALTER TABLE mcp_endpoints ADD COLUMN IF NOT EXISTS kind VARCHAR(16) NOT NULL DEFAULT 'space';
ALTER TABLE mcp_endpoints ADD COLUMN IF NOT EXISTS user_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_mcp_endpoints_owner
    ON mcp_endpoints (tenant_id, kind, user_id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS mcp_endpoint_settings (
    tenant_id BIGINT PRIMARY KEY,
    personal_rate_limit_per_minute INTEGER NOT NULL DEFAULT 60,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

COMMENT ON COLUMN mcp_endpoints.kind IS 'space (admin-published) or personal (acts as user_id)';
COMMENT ON COLUMN mcp_endpoints.user_id IS 'Owning member of a personal endpoint; empty for space endpoints';
COMMENT ON COLUMN mcp_endpoint_settings.personal_rate_limit_per_minute IS 'Tool calls per minute shared by every personal endpoint in the workspace';
