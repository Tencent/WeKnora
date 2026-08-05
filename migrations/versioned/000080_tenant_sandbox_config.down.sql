DO $$ BEGIN RAISE NOTICE '[Migration 000080 down] Dropping tenant_sandbox_configs'; END $$;

DROP TABLE IF EXISTS tenant_sandbox_configs;
