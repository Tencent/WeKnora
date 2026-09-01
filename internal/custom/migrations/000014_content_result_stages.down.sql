DROP INDEX IF EXISTS idx_video_processing_jobs_result_stage;

ALTER TABLE video_processing_jobs
    DROP COLUMN IF EXISTS result_stage;

ALTER TABLE videos
    DROP COLUMN IF EXISTS summary_result_stage,
    DROP COLUMN IF EXISTS summary_draft_wiki_page_id,
    DROP COLUMN IF EXISTS outline_result_stage,
    DROP COLUMN IF EXISTS outline_draft_wiki_page_id;
