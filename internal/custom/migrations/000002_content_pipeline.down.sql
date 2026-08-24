-- 回滚 000002

DROP INDEX IF EXISTS idx_videos_kb_wiki;
ALTER TABLE videos DROP COLUMN IF EXISTS knowledge_base_wiki_page_id;