CREATE TABLE IF NOT EXISTS embedding_cache_entries (
    tenant_id INTEGER NOT NULL,
    cache_key TEXT NOT NULL,
    model_id TEXT NOT NULL,
    vector BLOB NOT NULL,
    expires_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, cache_key)
);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_entries_expiry ON embedding_cache_entries (expires_at);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_entries_tenant_model ON embedding_cache_entries (tenant_id, model_id);
