CREATE TABLE IF NOT EXISTS model_usages (
    id BIGSERIAL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    model VARCHAR(255) NOT NULL,
    call_type VARCHAR(32) NOT NULL DEFAULT 'chat',
    actual_calls INTEGER NOT NULL DEFAULT 0,
    purpose VARCHAR(128) NOT NULL DEFAULT '',
    input_count INTEGER NOT NULL DEFAULT 0,
    local_cache_hits INTEGER NOT NULL DEFAULT 0,
    prompt_fingerprint VARCHAR(128) NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cache_miss_tokens INTEGER NOT NULL DEFAULT 0,
    cache_reported BOOLEAN NOT NULL DEFAULT FALSE,
    cache_status VARCHAR(32) NOT NULL DEFAULT 'unreported',
    cost_cny NUMERIC(20,8),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_model_usage_tenant_created ON model_usages (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_model ON model_usages (model);
