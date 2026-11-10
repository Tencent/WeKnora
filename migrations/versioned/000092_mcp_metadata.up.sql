ALTER TABLE mcp_services ADD COLUMN IF NOT EXISTS usage_instructions TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS mcp_metadata (
    tenant_id BIGINT NOT NULL,
    service_id VARCHAR(36) NOT NULL REFERENCES mcp_services(id) ON DELETE CASCADE,
    principal VARCHAR(255) NOT NULL DEFAULT '',
    config_fingerprint VARCHAR(64) NOT NULL,
    tools JSONB NOT NULL,
    instructions TEXT NOT NULL DEFAULT '',
    server_name TEXT NOT NULL DEFAULT '',
    server_version TEXT NOT NULL DEFAULT '',
    server_description TEXT NOT NULL DEFAULT '',
    synced_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, service_id, principal)
);
