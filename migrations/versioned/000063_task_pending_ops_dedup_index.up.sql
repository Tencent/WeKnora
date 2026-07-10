CREATE INDEX IF NOT EXISTS idx_task_pending_ops_dedup
    ON task_pending_ops (task_type, scope, scope_id, dedup_key, op);
