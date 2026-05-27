-- Rollback: drop system_settings table and users.is_system_admin column

DO $$ BEGIN RAISE NOTICE '[Migration 000053 DOWN] Dropping table: system_settings'; END $$;

DROP TABLE IF EXISTS system_settings CASCADE;

DO $$ BEGIN RAISE NOTICE '[Migration 000053 DOWN] Dropping users.is_system_admin...'; END $$;

DROP INDEX IF EXISTS idx_users_is_system_admin;

ALTER TABLE users DROP COLUMN IF EXISTS is_system_admin;

DO $$ BEGIN RAISE NOTICE '[Migration 000053 DOWN] Done.'; END $$;
