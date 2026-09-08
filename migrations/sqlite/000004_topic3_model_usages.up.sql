CREATE TABLE IF NOT EXISTS model_usages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER NOT NULL, model TEXT NOT NULL,
    call_type TEXT NOT NULL DEFAULT 'chat', actual_calls INTEGER NOT NULL DEFAULT 0, input_count INTEGER NOT NULL DEFAULT 0, local_cache_hits INTEGER NOT NULL DEFAULT 0,
    purpose TEXT NOT NULL DEFAULT '', prompt_fingerprint TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0, completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0, cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0, cache_miss_tokens INTEGER NOT NULL DEFAULT 0,
    cache_reported INTEGER NOT NULL DEFAULT 0, cache_status TEXT NOT NULL DEFAULT 'unreported',
    cost_cny NUMERIC, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_model_usage_tenant_created ON model_usages (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_model ON model_usages (model);
