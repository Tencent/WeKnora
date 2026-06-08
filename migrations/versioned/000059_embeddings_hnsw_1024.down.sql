-- Down migration: drop the 1024-dim HNSW partial index added in 000059.
DROP INDEX IF EXISTS embeddings_embedding_idx_1024;
