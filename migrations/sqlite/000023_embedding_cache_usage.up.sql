CREATE TABLE IF NOT EXISTS embedding_cache_usage_events (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    model_id TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL DEFAULT '',
    lookup_count INTEGER NOT NULL DEFAULT 0,
    hit_count INTEGER NOT NULL DEFAULT 0,
    miss_count INTEGER NOT NULL DEFAULT 0,
    deduplicated_count INTEGER NOT NULL DEFAULT 0,
    avoided_computations INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_usage_tenant_created
    ON embedding_cache_usage_events (tenant_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_usage_tenant_model_created
    ON embedding_cache_usage_events (tenant_id, model_id, created_at DESC);
