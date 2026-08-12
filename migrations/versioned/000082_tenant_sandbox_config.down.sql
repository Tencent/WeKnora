DO $$ BEGIN RAISE NOTICE '[Migration 000082 down] Dropping tenant_sandbox_configs'; END $$;

DROP TABLE IF EXISTS tenant_sandbox_configs;
