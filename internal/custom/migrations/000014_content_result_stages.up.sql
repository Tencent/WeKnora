ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS outline_draft_wiki_page_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS outline_result_stage VARCHAR(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary_draft_wiki_page_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary_result_stage VARCHAR(16) NOT NULL DEFAULT '';

ALTER TABLE video_processing_jobs
    ADD COLUMN IF NOT EXISTS result_stage VARCHAR(16) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_video_processing_jobs_result_stage
    ON video_processing_jobs (result_stage);
