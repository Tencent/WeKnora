ALTER TABLE evaluation_tasks
    ADD COLUMN IF NOT EXISTS run_config JSONB NOT NULL DEFAULT '{}'::jsonb;
