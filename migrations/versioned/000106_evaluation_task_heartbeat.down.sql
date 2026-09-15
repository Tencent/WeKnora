DROP INDEX IF EXISTS idx_evaluation_tasks_worker_heartbeat;
ALTER TABLE evaluation_tasks DROP COLUMN IF EXISTS heartbeat_at;
ALTER TABLE evaluation_tasks DROP COLUMN IF EXISTS worker_id;
