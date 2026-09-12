DROP INDEX IF EXISTS idx_evaluation_tasks_tenant_status_started;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_status
    ON evaluation_tasks (tenant_id, status, start_time DESC)
    WHERE deleted_at IS NULL;
