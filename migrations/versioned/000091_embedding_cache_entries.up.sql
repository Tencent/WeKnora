CREATE TABLE IF NOT EXISTS embedding_cache_entries (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    model_id VARCHAR(128) NOT NULL,
    dimension INTEGER NOT NULL DEFAULT 0,
    text_hash VARCHAR(64) NOT NULL,
    vector JSONB NOT NULL,
    hits BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, model_id, dimension, text_hash)
);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_tenant
    ON embedding_cache_entries(tenant_id);

CREATE INDEX IF NOT EXISTS idx_embedding_cache_model
    ON embedding_cache_entries(model_id);
