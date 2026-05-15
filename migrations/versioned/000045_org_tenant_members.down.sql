-- Reverse migration for 000045_org_tenant_members.
--
-- Restore organization_members from the parked _pre_plan3 table and drop
-- the new tenant-scoped table. Because we only RENAMEd the original on
-- the way up, no data is lost on rollback — the legacy rows are still
-- intact under the renamed table.
--
-- If the parked table no longer exists (e.g. a later destructive
-- migration already dropped it), this script falls through gracefully:
-- the IF EXISTS guards leave the database in whatever state it was in.

DO $$ BEGIN RAISE NOTICE '[Migration 000045] Reverting Plan 3'; END $$;

-- Move legacy table back into place.
ALTER TABLE IF EXISTS organization_members_pre_plan3 RENAME TO organization_members;

-- Restore index names so the application code can find them by their
-- documented identifiers.
ALTER INDEX IF EXISTS idx_org_members_org_user_pre_plan3      RENAME TO idx_org_members_org_user;
ALTER INDEX IF EXISTS idx_org_members_user_id_pre_plan3       RENAME TO idx_org_members_user_id;
ALTER INDEX IF EXISTS idx_org_members_tenant_id_pre_plan3     RENAME TO idx_org_members_tenant_id;
ALTER INDEX IF EXISTS idx_org_members_role_pre_plan3          RENAME TO idx_org_members_role;

-- Drop the new table. The unique/secondary indexes go with it.
DROP TABLE IF EXISTS organization_tenant_members;

DO $$ BEGIN RAISE NOTICE '[Migration 000045] Plan 3 reverted'; END $$;
