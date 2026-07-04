-- Migration: 000039_vlm_cache
-- Description: Create vlm_cache table for storing VLM OCR and Caption results.

DO $$ BEGIN RAISE NOTICE '[Migration 000039] Creating vlm_cache table'; END $$;

CREATE TABLE IF NOT EXISTS vlm_cache (
    image_hash       CHAR(64)     NOT NULL,
    vlm_model_id     VARCHAR(128) NOT NULL,
    prompt_version   VARCHAR(64)  NOT NULL,
    result_text      TEXT         NOT NULL,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_accessed_at TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (image_hash, vlm_model_id, prompt_version)
);

CREATE INDEX IF NOT EXISTS idx_vlm_cache_lru ON vlm_cache (last_accessed_at);
DO $$ BEGIN RAISE NOTICE '[Migration 000039] vlm_cache table created successfully'; END $$;