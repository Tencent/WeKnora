ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS summary_wiki_page_version INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS summary_source VARCHAR(32) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary_knowledge_enhanced BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS summary_user_edited BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE videos
SET video_type = CASE video_type
    WHEN 'tutorial' THEN 'training'
    WHEN 'lecture' THEN 'salon'
    WHEN 'case_analysis' THEN 'general'
    WHEN '' THEN 'general'
    ELSE video_type
END
WHERE video_type IN ('tutorial', 'lecture', 'case_analysis', '');
