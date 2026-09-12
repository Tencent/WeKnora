-- Mirrors versioned migration 000093_evaluation_task_retention.

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_retention
    ON evaluation_tasks (end_time)
    WHERE status IN (2, 3, 4, 5, 6) AND end_time IS NOT NULL;
