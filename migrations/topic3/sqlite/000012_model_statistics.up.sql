CREATE TABLE IF NOT EXISTS embedding_cache_lookup_records (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    model_id TEXT NOT NULL,
    requested_items INTEGER NOT NULL,
    unique_items INTEGER NOT NULL,
    hit_items INTEGER NOT NULL,
    miss_items INTEGER NOT NULL,
    bypass_items INTEGER NOT NULL,
    status TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    occurred_at DATETIME NOT NULL,
    CONSTRAINT embedding_cache_lookup_records_counts_check CHECK (
        requested_items > 0 AND unique_items > 0 AND unique_items <= requested_items
        AND hit_items >= 0 AND miss_items >= 0 AND bypass_items >= 0
        AND hit_items + miss_items + bypass_items = unique_items
        AND duration_ms >= 0
    ),
    CONSTRAINT embedding_cache_lookup_records_status_check CHECK (
        (status = 'hit' AND hit_items = unique_items AND miss_items = 0 AND bypass_items = 0)
        OR (status = 'miss' AND hit_items = 0 AND miss_items = unique_items AND bypass_items = 0)
        OR (status = 'partial' AND hit_items > 0 AND miss_items > 0 AND bypass_items = 0)
        OR (status = 'bypass' AND hit_items = 0 AND miss_items = 0 AND bypass_items = unique_items)
    )
);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_lookup_tenant_model_time
    ON embedding_cache_lookup_records (tenant_id, model_id, occurred_at DESC);
