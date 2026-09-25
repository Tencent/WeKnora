-- Rollback: 000075_wiki_page_revisions
DO $$ BEGIN RAISE NOTICE '[Migration 000075] Reverting wiki page revisions schema'; END $$;

DROP TABLE IF EXISTS wiki_page_revisions;

ALTER TABLE wiki_pages DROP COLUMN IF EXISTS last_edit_source;
ALTER TABLE wiki_pages DROP COLUMN IF EXISTS last_editor_id;
