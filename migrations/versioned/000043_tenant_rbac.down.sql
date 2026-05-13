-- Migration 000043 down: revert tenant RBAC schema additions.
-- Note: this drops role information unrecoverably; only intended for
-- development rollbacks, not production.
DO $$ BEGIN RAISE NOTICE '[Migration 000043 down] Reverting tenant RBAC...'; END $$;

ALTER TABLE custom_agents DROP COLUMN IF EXISTS runnable_by_viewer;

DROP INDEX IF EXISTS idx_knowledge_bases_tenant_creator;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS creator_id;

DROP INDEX IF EXISTS idx_tenant_members_user;
DROP INDEX IF EXISTS idx_tenant_members_tenant_role;
DROP INDEX IF EXISTS idx_tenant_members_user_tenant_unique;
DROP TABLE IF EXISTS tenant_members;

DO $$ BEGIN RAISE NOTICE '[Migration 000043 down] tenant RBAC reverted'; END $$;
