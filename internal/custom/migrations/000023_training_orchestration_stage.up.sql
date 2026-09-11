ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS stage VARCHAR(32);
