DROP INDEX IF EXISTS idx_evaluation_tasks_worker_heartbeat;
ALTER TABLE evaluation_tasks DROP COLUMN heartbeat_at;
ALTER TABLE evaluation_tasks DROP COLUMN worker_id;
