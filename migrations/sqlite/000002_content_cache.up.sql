-- Migration 000002: content-addressed cache table (SQLite).
CREATE TABLE IF NOT EXISTS content_cache (
    cache_key VARCHAR(128) PRIMARY KEY,
    kind VARCHAR(32) NOT NULL DEFAULT '',
    payload TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_content_cache_kind ON content_cache (kind);
CREATE INDEX IF NOT EXISTS idx_content_cache_updated_at ON content_cache (updated_at);
