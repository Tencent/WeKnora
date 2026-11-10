-- Migration: 000041_task_queue_and_wiki_indexes (rollback)
-- Description: Drop the generic task queue tables and the new wiki_pages
--              GIN indexes. Pending rows are NOT migrated back to Redis on
--              rollback; operators are expected to drain the queue first
--              (or accept the loss of un-consumed ops).

DO $$ BEGIN RAISE NOTICE '[Migration 000041 DOWN] Reverting task queue + wiki indexes schema'; END $$;

DROP INDEX IF EXISTS idx_wiki_pages_title_trgm;
DROP INDEX IF EXISTS idx_wiki_pages_source_refs_text;
DROP INDEX IF EXISTS idx_wiki_pages_source_refs;

DROP TABLE IF EXISTS task_dead_letters;
DROP TABLE IF EXISTS task_pending_ops;

DO $$ BEGIN RAISE NOTICE '[Migration 000041 DOWN] task queue + wiki indexes schema reverted successfully'; END $$;
