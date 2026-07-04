-- Migration: 000081_embedding_cache
-- Description: Create embedding_cache table to store vector blobs for cross-document deduplication
-- This acts as Layer 2 cache to freeze the outputs of embedding models
DO $$ BEGIN RAISE NOTICE '[Migration 000081] Creating embedding_cache table'; END $$;

CREATE TABLE IF NOT EXISTS embedding_cache (
    content_hash     CHAR(64)     NOT NULL,
    model_id         VARCHAR(128) NOT NULL,
    dimension        INT          NOT NULL,
    vector_data      BYTEA        NOT NULL,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_accessed_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (content_hash, model_id, dimension)
);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_lru ON embedding_cache (last_accessed_at);
DO $$ BEGIN RAISE NOTICE '[Migration 000081] embedding_cache table created successfully'; END $$;
