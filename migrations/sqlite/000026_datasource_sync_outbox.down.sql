DROP INDEX IF EXISTS idx_sync_logs_outbox_dispatch;
DROP INDEX IF EXISTS idx_sync_logs_task_id;
ALTER TABLE sync_logs DROP COLUMN last_dispatch_error;
ALTER TABLE sync_logs DROP COLUMN next_dispatch_at;
ALTER TABLE sync_logs DROP COLUMN dispatch_attempts;
ALTER TABLE sync_logs DROP COLUMN dispatched_at;
ALTER TABLE sync_logs DROP COLUMN task_payload;
ALTER TABLE sync_logs DROP COLUMN task_id;
