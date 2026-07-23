CREATE TABLE IF NOT EXISTS content_caches (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    cache_kind VARCHAR(32) NOT NULL,
    cache_key VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_content_caches_key
    ON content_caches (tenant_id, cache_kind, cache_key);
