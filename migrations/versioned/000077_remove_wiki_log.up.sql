-- Migration: 000077_remove_wiki_log
-- Description: Remove the duplicate Wiki-only operation feed. Knowledge-base
--              activity is the authoritative mutation history, and Wiki
--              retrieval never reads this table.

DO $$ BEGIN RAISE NOTICE '[Migration 000077] Removing wiki operation log storage'; END $$;

DROP TABLE IF EXISTS wiki_log_entries;

-- Very old installations may still have the pre-000040 log.md row. It is not
-- backed by the activity feed and the model no longer treats it as a special
-- retrieval page, so remove it with the obsolete page type.
DELETE FROM wiki_pages WHERE page_type = 'log';

DO $$ BEGIN RAISE NOTICE '[Migration 000077] Wiki operation log storage removed'; END $$;
