CREATE TABLE IF NOT EXISTS evaluation_tasks (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    dataset_id TEXT NOT NULL,
    status INTEGER NOT NULL,
    err_msg TEXT NOT NULL DEFAULT '',
    total INTEGER NOT NULL DEFAULT 0,
    finished INTEGER NOT NULL DEFAULT 0,
    params TEXT NOT NULL,
    metric TEXT,
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    duration_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_started_at
    ON evaluation_tasks (tenant_id, started_at DESC);
