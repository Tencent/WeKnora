DROP INDEX IF EXISTS idx_jobs_error_category;
DROP INDEX IF EXISTS idx_jobs_stage_generation;
DROP INDEX IF EXISTS idx_jobs_video_generation;

ALTER TABLE video_processing_jobs
    DROP COLUMN IF EXISTS error_category,
    DROP COLUMN IF EXISTS transcript_generation;
