-- Roll back SQLite migration 000002.
-- knowledge_processing_spans is intentionally retained: this migration also
-- repairs a table missing from the SQLite baseline for an existing subsystem,
-- and deleting its trace history during a Wiki rollback would be destructive.

DROP TABLE IF EXISTS knowledge_subtask_settlements;
DROP TABLE IF EXISTS wiki_block_sources;
DROP TABLE IF EXISTS wiki_page_blocks;
DROP TABLE IF EXISTS wiki_page_block_sets;

DROP INDEX IF EXISTS idx_wiki_page_revisions_block_set;
DROP INDEX IF EXISTS idx_wiki_pages_current_block_set;

ALTER TABLE wiki_page_revisions DROP COLUMN block_set_id;
ALTER TABLE wiki_pages DROP COLUMN current_block_set_id;
