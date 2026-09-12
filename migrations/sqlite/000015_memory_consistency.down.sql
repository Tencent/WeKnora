DROP INDEX IF EXISTS idx_memory_replaces;
DROP TABLE IF EXISTS memory_extraction_sessions;
ALTER TABLE memory_items DROP COLUMN replaces_id;
ALTER TABLE memory_subjects DROP COLUMN extraction_state;
