DROP INDEX IF EXISTS idx_knowledges_folder;
ALTER TABLE knowledges DROP COLUMN IF EXISTS folder_id;

DROP INDEX IF EXISTS uni_knowledge_folders_kb_parent_name;
DROP INDEX IF EXISTS idx_knowledge_folders_path;
DROP INDEX IF EXISTS idx_knowledge_folders_parent;
DROP INDEX IF EXISTS idx_knowledge_folders_kb;
DROP TABLE IF EXISTS knowledge_folders;
