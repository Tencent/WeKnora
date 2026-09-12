-- Mirrors versioned migration 000092_evaluation_task_list.

DROP INDEX IF EXISTS idx_evaluation_tasks_tenant_status;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_status_started
    ON evaluation_tasks (tenant_id, status, start_time DESC, id DESC)
    WHERE deleted_at IS NULL;
