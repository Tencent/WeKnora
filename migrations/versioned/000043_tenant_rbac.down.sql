-- Migration 000043 down: revert tenant RBAC schema additions.
--
-- DANGER: This drops the tenant_members table and the creator_id /
--         runnable_by_viewer columns. All role assignments, KB ownership
--         metadata, and viewer-runtime flags are lost UNRECOVERABLY.
--         Only intended for development rollbacks, never for production.
--
-- Operators who really need to run this in a non-dev environment must set
-- the session-level GUC `weknora.allow_destructive_migration` to 'true'
-- first (e.g. `SET weknora.allow_destructive_migration = 'true';`).
-- Otherwise the migration aborts before touching any data.
DO $$
BEGIN
    IF coalesce(current_setting('weknora.allow_destructive_migration', true), 'false') <> 'true' THEN
        RAISE EXCEPTION
            '[Migration 000043 down] BLOCKED: refuse to drop tenant_members / creator_id / runnable_by_viewer. '
            'Set weknora.allow_destructive_migration = ''true'' in this session to override.';
    END IF;
    RAISE WARNING '[Migration 000043 down] Reverting tenant RBAC — role data will be lost';
END $$;

ALTER TABLE custom_agents DROP COLUMN IF EXISTS runnable_by_viewer;

DROP INDEX IF EXISTS idx_knowledge_bases_tenant_creator;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS creator_id;

DROP INDEX IF EXISTS idx_tenant_members_user;
DROP INDEX IF EXISTS idx_tenant_members_tenant_role;
DROP INDEX IF EXISTS idx_tenant_members_user_tenant_unique;
DROP TABLE IF EXISTS tenant_members;

DO $$ BEGIN RAISE NOTICE '[Migration 000043 down] tenant RBAC reverted'; END $$;
