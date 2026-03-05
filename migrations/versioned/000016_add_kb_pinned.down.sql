-- Remove pin (置顶) support from knowledge bases
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS pinned_at;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS is_pinned;
