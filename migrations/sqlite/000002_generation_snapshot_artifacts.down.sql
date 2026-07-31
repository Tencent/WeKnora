DROP TABLE IF EXISTS processing_artifacts;
DROP TABLE IF EXISTS knowledge_generations;

DROP INDEX IF EXISTS uk_chunks_generation_logical;
DROP INDEX IF EXISTS idx_chunks_active_generation;
ALTER TABLE chunks DROP COLUMN artifact_digest;
ALTER TABLE chunks DROP COLUMN logical_chunk_key;
ALTER TABLE chunks DROP COLUMN generation_id;

DROP INDEX IF EXISTS idx_knowledges_active_generation;
ALTER TABLE knowledges DROP COLUMN active_generation_id;
