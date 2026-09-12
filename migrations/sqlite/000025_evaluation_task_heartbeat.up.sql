ALTER TABLE evaluation_tasks ADD COLUMN worker_id TEXT NOT NULL DEFAULT '';
ALTER TABLE evaluation_tasks ADD COLUMN heartbeat_at DATETIME;
CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_worker_heartbeat
    ON evaluation_tasks (worker_id, heartbeat_at);
