-- SQLite: mirror 000085_mcp_org_unit_visibility

ALTER TABLE mcp_services
    ADD COLUMN org_unit_id TEXT NOT NULL DEFAULT '';

ALTER TABLE mcp_services
    ADD COLUMN share_with_descendants INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_mcp_services_tenant_org_unit
    ON mcp_services (tenant_id, org_unit_id)
    WHERE deleted_at IS NULL AND is_builtin = 0;

CREATE TABLE IF NOT EXISTS mcp_shares (
    id TEXT PRIMARY KEY,
    mcp_service_id TEXT NOT NULL,
    organization_id TEXT NOT NULL,
    shared_by_user_id TEXT NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission TEXT NOT NULL DEFAULT 'viewer',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_shares_svc_org
    ON mcp_shares(mcp_service_id, organization_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mcp_shares_svc_id ON mcp_shares(mcp_service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_shares_org_id ON mcp_shares(organization_id);
CREATE INDEX IF NOT EXISTS idx_mcp_shares_source_tenant ON mcp_shares(source_tenant_id);
CREATE INDEX IF NOT EXISTS idx_mcp_shares_deleted_at ON mcp_shares(deleted_at);
