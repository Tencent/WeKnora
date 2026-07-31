-- Migration 000079: content-addressed cache for deterministic pipeline products.
-- Stores VLM OCR/Caption, embeddings, wiki per-document maps, per-chunk graph
-- extractions, summaries and questions keyed by hash(content + model + prompt
-- version + config), so a reparse of unchanged content reuses prior results.
CREATE TABLE IF NOT EXISTS content_cache (
    cache_key VARCHAR(128) PRIMARY KEY,
    kind VARCHAR(32) NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_content_cache_kind ON content_cache (kind);
CREATE INDEX IF NOT EXISTS idx_content_cache_updated_at ON content_cache (updated_at);
