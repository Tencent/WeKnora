-- CP-T004 / CP-T005 / CP-T006：内容生产管线新增字段
-- 给 videos 加 knowledge_base_wiki_page_id（extract-video-knowledge 产物「知识底座」索引页 ID）

ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS knowledge_base_wiki_page_id VARCHAR(64);

-- 索引：方便按视频查「知识底座」页（虽然一般按主键查，但保留给后台聚合扫描用）
CREATE INDEX IF NOT EXISTS idx_videos_kb_wiki ON videos(knowledge_base_wiki_page_id)
    WHERE knowledge_base_wiki_page_id IS NOT NULL;