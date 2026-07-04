-- Migration: 0000040_embedding_cache
-- Description: Create embedding_cache table to store vector blobs for cross-document deduplication
-- This acts as Layer2 cache to freeze the outputs of Embedding modles
Do $$ BEGIN RAISE NOTICE '[Migration: 0000040] Createing embedding_cache table';END $$;

CREATE TABLE IF NOT EXISTS embedding_cache (
    content_hash     CHAR(64)     NOT NULL, 
    model_id         VARCHAR(128) NOT NULL,
    dimension        INT          NOT NULL, 
    vector_data      BYTEA        NOT NULL, 
    created_at       TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_accessed_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (content_hash, model_id, dimension)
);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_lru ON embedding_cache (last_accessed_at);
DO $$ BEGIN RAISE NOTICE '[Migration 000040] embedding_cache table created successfully'; END $$;