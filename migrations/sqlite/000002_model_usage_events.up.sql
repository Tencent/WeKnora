CREATE TABLE IF NOT EXISTS model_usage_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    model_name VARCHAR(255) NOT NULL DEFAULT '',
    model_type VARCHAR(32) NOT NULL DEFAULT '',
    model_source VARCHAR(32) NOT NULL DEFAULT '',
    provider VARCHAR(64) NOT NULL DEFAULT '',
    request_kind VARCHAR(64) NOT NULL DEFAULT '',
    usage_source VARCHAR(32) NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    input_items INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    success INTEGER NOT NULL DEFAULT 1,
    error_type VARCHAR(128) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_model_usage_events_tenant_time
    ON model_usage_events (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_events_tenant_model_time
    ON model_usage_events (tenant_id, model_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_model_usage_events_tenant_type_time
    ON model_usage_events (tenant_id, model_type, created_at DESC);
