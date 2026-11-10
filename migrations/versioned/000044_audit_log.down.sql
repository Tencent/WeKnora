-- Migration 000044 down: drop audit_logs table.
--
-- DANGER: This drops the audit_logs table. All durable RBAC and
--         resource-action history is lost UNRECOVERABLY. Only intended
--         for development rollbacks, never for production.
--
-- Operators who really need to run this in a non-dev environment must
-- set the session-level GUC `weknora.allow_destructive_migration` to
-- 'true' first. Otherwise the migration aborts before touching any data.
DO $$
BEGIN
    IF coalesce(current_setting('weknora.allow_destructive_migration', true), 'false') <> 'true' THEN
        RAISE EXCEPTION
            '[Migration 000044 down] BLOCKED: refuse to drop audit_logs. '
            'Set weknora.allow_destructive_migration = ''true'' in this session to override.';
    END IF;
    RAISE WARNING '[Migration 000044 down] Dropping audit_logs — durable history will be lost';
END $$;

DROP INDEX IF EXISTS idx_audit_logs_created_at;
DROP INDEX IF EXISTS idx_audit_logs_tenant_action;
DROP INDEX IF EXISTS idx_audit_logs_actor;
DROP INDEX IF EXISTS idx_audit_logs_tenant_id_desc;
DROP TABLE IF EXISTS audit_logs;

DO $$ BEGIN RAISE NOTICE '[Migration 000044 down] audit_logs dropped'; END $$;
