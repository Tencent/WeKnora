DROP INDEX IF EXISTS idx_knowledges_folder;
ALTER TABLE knowledges DROP COLUMN folder_id;
DROP TABLE IF EXISTS knowledge_folder_closure;
DROP TABLE IF EXISTS knowledge_folders;
