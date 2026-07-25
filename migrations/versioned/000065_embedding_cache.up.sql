-- Migration: embedding_cache
-- Description: Create embedding_cache table for cross-document embedding reuse
-- This cache maps hash(text + model_id + dimensions) → serialized []float32
-- so that identical content is never re-embedded, even across different documents
-- or across reparse operations.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'embedding_cache') THEN
        CREATE TABLE embedding_cache (
            cache_key   VARCHAR(64) PRIMARY KEY,
            model_id    VARCHAR(64) NOT NULL,
            dimensions  INTEGER NOT NULL,
            embedding   BYTEA NOT NULL,
            text_preview TEXT,
            created_at  TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
            updated_at  TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
        );
        CREATE INDEX idx_embedding_cache_model ON embedding_cache(model_id);
        RAISE NOTICE '[Migration 000065] Created embedding_cache table';
    ELSE
        RAISE NOTICE '[Migration 000065] embedding_cache table already exists, skipping';
    END IF;
END $$;
