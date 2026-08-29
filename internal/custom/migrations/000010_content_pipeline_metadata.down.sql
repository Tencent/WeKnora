ALTER TABLE videos
    DROP COLUMN IF EXISTS summary_wiki_page_version,
    DROP COLUMN IF EXISTS summary_source,
    DROP COLUMN IF EXISTS summary_knowledge_enhanced,
    DROP COLUMN IF EXISTS summary_user_edited;
