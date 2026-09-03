DO $$ BEGIN RAISE NOTICE '[Migration 000088 down] Dropping env vars'; END $$;

DROP TABLE IF EXISTS tenant_user_env_vars;
ALTER TABLE tenant_skills DROP COLUMN IF EXISTS envs;
