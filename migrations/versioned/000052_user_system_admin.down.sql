-- Rollback: drop users.is_system_admin column and its index

DO $$ BEGIN RAISE NOTICE '[Migration 000052 DOWN] Dropping users.is_system_admin...'; END $$;

DROP INDEX IF EXISTS idx_users_is_system_admin;

ALTER TABLE users DROP COLUMN IF EXISTS is_system_admin;

DO $$ BEGIN RAISE NOTICE '[Migration 000052 DOWN] Done.'; END $$;
