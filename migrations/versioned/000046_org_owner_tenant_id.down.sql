-- Reverse migration for 000046_org_owner_tenant_id.
--
-- Drop the index first, then the column. The data backfilled in up.sql
-- is recoverable from `users.tenant_id` so we don't park it.

DO $$ BEGIN RAISE NOTICE '[Migration 000046] Dropping organizations.owner_tenant_id'; END $$;

DROP INDEX IF EXISTS idx_organizations_owner_tenant;

ALTER TABLE organizations
    DROP COLUMN IF EXISTS owner_tenant_id;

DO $$ BEGIN RAISE NOTICE '[Migration 000046] organizations.owner_tenant_id removed'; END $$;
