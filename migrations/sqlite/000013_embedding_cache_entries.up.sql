CREATE TABLE IF NOT EXISTS embedding_cache_entries (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    model_id TEXT NOT NULL,
    dimension INTEGER NOT NULL DEFAULT 0,
    text_hash TEXT NOT NULL,
    vector TEXT NOT NULL,
    hits INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, model_id, dimension, text_hash)
);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_tenant
    ON embedding_cache_entries(tenant_id);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_model
    ON embedding_cache_entries(model_id);
