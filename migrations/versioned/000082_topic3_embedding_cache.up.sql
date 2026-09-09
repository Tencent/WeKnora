CREATE TABLE IF NOT EXISTS embedding_cache_entries (
    id BIGSERIAL PRIMARY KEY, tenant_id BIGINT NOT NULL, model_key VARCHAR(128) NOT NULL,
    input_hash VARCHAR(64) NOT NULL, vector JSONB NOT NULL, dimensions INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_embedding_cache_key UNIQUE (tenant_id, model_key, input_hash)
);
CREATE INDEX IF NOT EXISTS idx_embedding_cache_tenant_model ON embedding_cache_entries (tenant_id, model_key);
