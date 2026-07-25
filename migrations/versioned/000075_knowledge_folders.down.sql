DROP INDEX IF EXISTS idx_knowledges_kb_folder;
ALTER TABLE knowledges DROP COLUMN IF EXISTS folder_id;
DROP TABLE IF EXISTS knowledge_folders;
