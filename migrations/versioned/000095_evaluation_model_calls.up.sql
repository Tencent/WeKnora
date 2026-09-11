CREATE TABLE IF NOT EXISTS evaluation_model_calls (
    id VARCHAR(36) PRIMARY KEY,
    task_id VARCHAR(255) NOT NULL REFERENCES evaluation_tasks(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    model_id VARCHAR(255) NOT NULL DEFAULT '',
    model_name VARCHAR(255) NOT NULL,
    purpose VARCHAR(100) NOT NULL DEFAULT '',
    prompt_prefix_fingerprint VARCHAR(128) NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cache_miss_tokens INTEGER NOT NULL DEFAULT 0,
    cache_reported BOOLEAN NOT NULL DEFAULT FALSE,
    cache_status VARCHAR(32) NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    success BOOLEAN NOT NULL,
    err_msg TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_evaluation_model_calls_task_created_at
    ON evaluation_model_calls (task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_evaluation_model_calls_tenant_created_at
    ON evaluation_model_calls (tenant_id, created_at DESC);
