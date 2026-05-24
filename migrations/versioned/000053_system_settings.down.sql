-- Rollback: drop system_settings table
DO $$ BEGIN RAISE NOTICE '[Migration 000053 DOWN] Dropping table: system_settings'; END $$;

DROP TABLE IF EXISTS system_settings CASCADE;

DO $$ BEGIN RAISE NOTICE '[Migration 000053 DOWN] Done.'; END $$;
