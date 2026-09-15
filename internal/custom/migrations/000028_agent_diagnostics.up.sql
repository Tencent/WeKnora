ALTER TABLE video_processing_jobs
    ADD COLUMN IF NOT EXISTS agent_diagnostic TEXT NOT NULL DEFAULT '';
