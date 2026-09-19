DROP INDEX IF EXISTS idx_sync_logs_outbox_dispatch;
DROP INDEX IF EXISTS idx_sync_logs_task_id;
ALTER TABLE sync_logs DROP COLUMN IF EXISTS last_dispatch_error;
ALTER TABLE sync_logs DROP COLUMN IF EXISTS next_dispatch_at;
ALTER TABLE sync_logs DROP COLUMN IF EXISTS dispatch_attempts;
ALTER TABLE sync_logs DROP COLUMN IF EXISTS dispatched_at;
ALTER TABLE sync_logs DROP COLUMN IF EXISTS task_payload;
ALTER TABLE sync_logs DROP COLUMN IF EXISTS task_id;
