CREATE TABLE IF NOT EXISTS evaluation_model_calls (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES evaluation_tasks(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL,
    model_id TEXT NOT NULL DEFAULT '',
    model_name TEXT NOT NULL,
    purpose TEXT NOT NULL DEFAULT '',
    prompt_prefix_fingerprint TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    cache_miss_tokens INTEGER NOT NULL DEFAULT 0,
    cache_reported INTEGER NOT NULL DEFAULT 0,
    cache_status TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    success INTEGER NOT NULL,
    err_msg TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_evaluation_model_calls_task_created_at
    ON evaluation_model_calls (task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_evaluation_model_calls_tenant_created_at
    ON evaluation_model_calls (tenant_id, created_at DESC);
