CREATE TABLE IF NOT EXISTS evaluation_tasks (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    dataset_id VARCHAR(255) NOT NULL,
    status INTEGER NOT NULL,
    err_msg TEXT NOT NULL DEFAULT '',
    total INTEGER NOT NULL DEFAULT 0,
    finished INTEGER NOT NULL DEFAULT 0,
    params JSONB NOT NULL,
    metric JSONB,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    duration_ms BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_started_at
    ON evaluation_tasks (tenant_id, started_at DESC);
