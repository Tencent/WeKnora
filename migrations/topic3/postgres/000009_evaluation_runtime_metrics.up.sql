ALTER TABLE evaluation_tasks
    ADD COLUMN IF NOT EXISTS runtime_metrics JSONB;

ALTER TABLE evaluation_question_results
    ADD COLUMN IF NOT EXISTS usage_reported BOOLEAN NOT NULL DEFAULT FALSE;
