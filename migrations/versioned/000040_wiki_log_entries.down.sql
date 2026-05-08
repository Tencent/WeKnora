-- Migration: 000040_wiki_log_entries (rollback)
-- Description: Drop the wiki_log_entries table. No data migration was
--              performed on the way up, so rollback is a plain DROP.

DO $$ BEGIN RAISE NOTICE '[Migration 000040 DOWN] Reverting wiki_log_entries schema'; END $$;

DROP TABLE IF EXISTS wiki_log_entries;

DO $$ BEGIN RAISE NOTICE '[Migration 000040 DOWN] wiki_log_entries schema reverted successfully'; END $$;
