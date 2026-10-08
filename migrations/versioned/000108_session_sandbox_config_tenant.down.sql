DO $$ BEGIN RAISE NOTICE '[Migration 000108 down] Dropping sessions.sandbox_config_tenant_id'; END $$;

ALTER TABLE sessions DROP COLUMN IF EXISTS sandbox_config_tenant_id;
