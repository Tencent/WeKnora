ALTER TABLE training_orchestration_jobs
    DROP COLUMN IF EXISTS warning_message;

ALTER TABLE training_orchestration_jobs
    DROP COLUMN IF EXISTS warning_code;
