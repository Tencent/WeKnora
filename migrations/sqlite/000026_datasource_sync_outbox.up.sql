-- Mirrors versioned migration 000092 for Lite SQLite deployments.
ALTER TABLE sync_logs ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sync_logs ADD COLUMN task_payload TEXT;
ALTER TABLE sync_logs ADD COLUMN dispatched_at DATETIME;
ALTER TABLE sync_logs ADD COLUMN dispatch_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sync_logs ADD COLUMN next_dispatch_at DATETIME;
ALTER TABLE sync_logs ADD COLUMN last_dispatch_error TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_sync_logs_task_id ON sync_logs(task_id);
CREATE INDEX IF NOT EXISTS idx_sync_logs_outbox_dispatch
    ON sync_logs(next_dispatch_at, created_at)
    WHERE status = 'pending' AND dispatched_at IS NULL;
