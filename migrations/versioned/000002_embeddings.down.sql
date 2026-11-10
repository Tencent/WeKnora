-- Drop indexes for embeddings
DROP INDEX IF EXISTS idx_embeddings_knowledge_base_id;

-- Drop index
DROP INDEX IF EXISTS idx_embeddings_is_enabled;

DROP INDEX IF EXISTS embeddings_unique_source;
DROP INDEX IF EXISTS embeddings_search_idx;
DROP INDEX IF EXISTS embeddings_embedding_idx_3584;
DROP INDEX IF EXISTS embeddings_embedding_idx_798;
DROP INDEX IF EXISTS embeddings_embedding_idx;

-- Drop embeddings table
DROP TABLE IF EXISTS embeddings;
