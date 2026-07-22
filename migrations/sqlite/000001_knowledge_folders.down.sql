-- Rollback: 000001_knowledge_folders

DROP INDEX IF EXISTS idx_knowledges_folder_index_pending;
DROP INDEX IF EXISTS idx_knowledges_folder;

ALTER TABLE knowledges DROP COLUMN folder_indexed_version;
ALTER TABLE knowledges DROP COLUMN folder_version;
ALTER TABLE knowledges DROP COLUMN folder_id;

DROP INDEX IF EXISTS idx_knowledge_folders_deleted_at;
DROP INDEX IF EXISTS idx_knowledge_folders_path;
DROP INDEX IF EXISTS idx_knowledge_folders_parent;
DROP INDEX IF EXISTS idx_knowledge_folders_live_sibling_name;
DROP TABLE IF EXISTS knowledge_folders;
