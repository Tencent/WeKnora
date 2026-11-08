-- Mirrors versioned migration 000137_mcp_personal_endpoints.
ALTER TABLE mcp_endpoints ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'space';
ALTER TABLE mcp_endpoints ADD COLUMN user_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_mcp_endpoints_owner
    ON mcp_endpoints (tenant_id, kind, user_id)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS mcp_endpoint_settings (
    tenant_id INTEGER PRIMARY KEY,
    personal_rate_limit_per_minute INTEGER NOT NULL DEFAULT 60,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
