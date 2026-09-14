-- Migration: 000085_mcp_org_unit_visibility
-- Description: Align MCP service visibility with knowledge bases —
--              org_unit_id + share_with_descendants, plus cross-space mcp_shares.

DO $$ BEGIN RAISE NOTICE '[Migration 000085] Adding org-unit visibility to mcp_services...'; END $$;

ALTER TABLE mcp_services
    ADD COLUMN IF NOT EXISTS org_unit_id VARCHAR(36) NOT NULL DEFAULT '';

ALTER TABLE mcp_services
    ADD COLUMN IF NOT EXISTS share_with_descendants BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_mcp_services_tenant_org_unit
    ON mcp_services (tenant_id, org_unit_id)
    WHERE deleted_at IS NULL AND is_builtin = FALSE;

COMMENT ON COLUMN mcp_services.org_unit_id IS
    'Org unit stamped at create time; empty = legacy tenant-wide visibility';
COMMENT ON COLUMN mcp_services.share_with_descendants IS
    'When true, descendant OrgUnits may read this MCP service; default false';

DO $$ BEGIN RAISE NOTICE '[Migration 000085] Creating mcp_shares...'; END $$;

CREATE TABLE IF NOT EXISTS mcp_shares (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    mcp_service_id VARCHAR(36) NOT NULL,
    organization_id VARCHAR(36) NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    shared_by_user_id VARCHAR(36) NOT NULL,
    source_tenant_id INTEGER NOT NULL,
    permission VARCHAR(32) NOT NULL DEFAULT 'viewer',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_shares_svc_org
    ON mcp_shares(mcp_service_id, organization_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_mcp_shares_svc_id ON mcp_shares(mcp_service_id);
CREATE INDEX IF NOT EXISTS idx_mcp_shares_org_id ON mcp_shares(organization_id);
CREATE INDEX IF NOT EXISTS idx_mcp_shares_source_tenant ON mcp_shares(source_tenant_id);
CREATE INDEX IF NOT EXISTS idx_mcp_shares_deleted_at ON mcp_shares(deleted_at);

COMMENT ON TABLE mcp_shares IS 'MCP service sharing records to organizations';
COMMENT ON COLUMN mcp_shares.source_tenant_id IS 'Original tenant ID of the MCP service';
COMMENT ON COLUMN mcp_shares.permission IS 'Access permission: viewer or editor';

DO $$ BEGIN RAISE NOTICE '[Migration 000085] mcp org-unit visibility ready'; END $$;
