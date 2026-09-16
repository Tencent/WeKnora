-- Retention scans physically remove terminal tasks past their end_time (mirrors sqlite 000016).

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_retention
    ON evaluation_tasks (end_time)
    WHERE status IN (2, 3, 4, 5, 6) AND end_time IS NOT NULL;
