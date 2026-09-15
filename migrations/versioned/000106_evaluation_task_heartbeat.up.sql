ALTER TABLE evaluation_tasks
    ADD COLUMN IF NOT EXISTS worker_id VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_worker_heartbeat
    ON evaluation_tasks (worker_id, heartbeat_at);
