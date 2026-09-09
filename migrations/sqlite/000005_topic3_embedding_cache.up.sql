CREATE TABLE IF NOT EXISTS embedding_cache_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER NOT NULL, model_key TEXT NOT NULL,
    input_hash TEXT NOT NULL, vector TEXT NOT NULL, dimensions INTEGER NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, model_key, input_hash)
);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_tenant_model ON embedding_cache_entries (tenant_id, model_key);
