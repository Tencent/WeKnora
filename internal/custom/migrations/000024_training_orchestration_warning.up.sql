ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS warning_code VARCHAR(64);

ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS warning_message TEXT;
