ALTER TABLE evaluation_question_results
    DROP COLUMN IF EXISTS usage_reported;

ALTER TABLE evaluation_tasks
    DROP COLUMN IF EXISTS runtime_metrics;
