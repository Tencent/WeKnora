CREATE TABLE IF NOT EXISTS embedding_cache_entries (
    tenant_id BIGINT NOT NULL,
    cache_key VARCHAR(64) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    vector BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, cache_key)
);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_entries_expiry ON embedding_cache_entries (expires_at);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_entries_tenant_model ON embedding_cache_entries (tenant_id, model_id);
