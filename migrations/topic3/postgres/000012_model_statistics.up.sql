CREATE TABLE IF NOT EXISTS embedding_cache_lookup_records (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    requested_items BIGINT NOT NULL,
    unique_items BIGINT NOT NULL,
    hit_items BIGINT NOT NULL,
    miss_items BIGINT NOT NULL,
    bypass_items BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL,
    duration_ms BIGINT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
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
