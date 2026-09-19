-- Durable DB→Asynq outbox for datasource synchronization.
ALTER TABLE sync_logs ADD COLUMN IF NOT EXISTS task_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE sync_logs ADD COLUMN IF NOT EXISTS task_payload JSONB;
ALTER TABLE sync_logs ADD COLUMN IF NOT EXISTS dispatched_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE sync_logs ADD COLUMN IF NOT EXISTS dispatch_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sync_logs ADD COLUMN IF NOT EXISTS next_dispatch_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE sync_logs ADD COLUMN IF NOT EXISTS last_dispatch_error TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_sync_logs_task_id ON sync_logs(task_id);
CREATE INDEX IF NOT EXISTS idx_sync_logs_outbox_dispatch
    ON sync_logs(next_dispatch_at, created_at)
    WHERE status = 'pending' AND dispatched_at IS NULL;
