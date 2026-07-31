DROP TABLE IF EXISTS processing_artifacts;
DROP INDEX IF EXISTS idx_knowledge_generations_lookup;
DROP TABLE IF EXISTS knowledge_generations;

DROP INDEX IF EXISTS uk_chunks_generation_logical;
DROP INDEX IF EXISTS idx_chunks_active_generation;
ALTER TABLE chunks DROP COLUMN IF EXISTS artifact_digest;
ALTER TABLE chunks DROP COLUMN IF EXISTS logical_chunk_key;
ALTER TABLE chunks DROP COLUMN IF EXISTS generation_id;

DROP INDEX IF EXISTS idx_embeddings_visibility_key;
DROP INDEX IF EXISTS idx_embeddings_generation_id;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'embeddings') THEN
        ALTER TABLE embeddings DROP COLUMN IF EXISTS visibility_key;
        ALTER TABLE embeddings DROP COLUMN IF EXISTS generation_id;
    END IF;
END $$;

DROP INDEX IF EXISTS idx_knowledges_active_generation;
ALTER TABLE knowledges DROP COLUMN IF EXISTS active_generation_id;
