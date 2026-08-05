DO $$ BEGIN RAISE NOTICE '[Migration 000081 down] Dropping sessions.sandbox_config_id'; END $$;

ALTER TABLE sessions DROP COLUMN IF EXISTS sandbox_config_id;
