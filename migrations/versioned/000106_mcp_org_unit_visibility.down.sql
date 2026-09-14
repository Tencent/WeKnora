-- Rollback: 000085_mcp_org_unit_visibility

DO $$ BEGIN RAISE NOTICE '[Migration 000085 DOWN] Dropping mcp_shares and org-unit columns...'; END $$;

DROP TABLE IF EXISTS mcp_shares;

DROP INDEX IF EXISTS idx_mcp_services_tenant_org_unit;

ALTER TABLE mcp_services
    DROP COLUMN IF EXISTS share_with_descendants;

ALTER TABLE mcp_services
    DROP COLUMN IF EXISTS org_unit_id;

DO $$ BEGIN RAISE NOTICE '[Migration 000085 DOWN] Done'; END $$;
