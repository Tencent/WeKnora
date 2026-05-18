-- Rollback: drop users.preferences (data loss is acceptable; it only
-- carries UI preferences, not anything users can't reset by clicking).

DO $$ BEGIN RAISE NOTICE '[Migration 000049 DOWN] Dropping users.preferences...'; END $$;

ALTER TABLE users DROP COLUMN IF EXISTS preferences;

DO $$ BEGIN RAISE NOTICE '[Migration 000049 DOWN] Done.'; END $$;
