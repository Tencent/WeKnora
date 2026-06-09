-- Migration 000060: Embedding cache for redundant computation reduction
--
-- Introduces an optional, transparent cache layer that stores previously
-- computed embeddings keyed by (content_hash, model_id, dimensions,
-- chunk_config_hash). When enabled, unchanged chunks skip the embedding
-- API call entirely during re-indexing.
--
-- The cache is opt-in (ENABLE_EMBEDDING_CACHE=false by default) and does
-- not affect ingestion semantics or pipeline behavior.

-- chunk_config_hash is stored as a reserved dimension for future cache
-- partitioning across different chunking strategies (e.g. chunk_size=512 vs
-- 1024). In this initial version it is set to a constant empty string and is
-- intentionally not part of the primary key, as the cache currently operates
-- at the (content_hash, model_id, dimensions) level. A future migration can
-- extend the uniqueness constraint when chunk-config-aware caching is added.

CREATE TABLE IF NOT EXISTS embedding_cache (
    content_hash      VARCHAR(64) NOT NULL,
    model_id          VARCHAR(64) NOT NULL,
    dimensions        INT NOT NULL,
    chunk_config_hash VARCHAR(64) NOT NULL DEFAULT '',
    embedding         BYTEA NOT NULL,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (content_hash, model_id, dimensions)
);

CREATE INDEX IF NOT EXISTS idx_emb_cache_model ON embedding_cache(model_id);
