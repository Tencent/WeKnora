ALTER TABLE evaluation_tasks
    ADD COLUMN IF NOT EXISTS usage JSONB NOT NULL DEFAULT '{}'::jsonb;
