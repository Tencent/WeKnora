ALTER TABLE video_processing_jobs
    ADD COLUMN IF NOT EXISTS transcript_generation VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_category VARCHAR(50) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_jobs_video_generation
    ON video_processing_jobs(video_id, transcript_generation, updated_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_stage_generation
    ON video_processing_jobs(video_id, job_type, transcript_generation)
    WHERE transcript_generation <> '';

CREATE INDEX IF NOT EXISTS idx_jobs_error_category
    ON video_processing_jobs(error_category)
    WHERE error_category <> '';
