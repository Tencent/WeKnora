-- Rollback: 000071_knowledge_folders

DROP INDEX IF EXISTS idx_knowledges_folder_index_pending;
DROP INDEX IF EXISTS idx_knowledges_folder;

ALTER TABLE knowledges
    DROP COLUMN IF EXISTS folder_indexed_version,
    DROP COLUMN IF EXISTS folder_version,
    DROP COLUMN IF EXISTS folder_id;

DROP INDEX IF EXISTS idx_knowledge_folders_deleted_at;
DROP INDEX IF EXISTS idx_knowledge_folders_path;
DROP INDEX IF EXISTS idx_knowledge_folders_parent;
DROP INDEX IF EXISTS idx_knowledge_folders_live_sibling_name;
DROP TABLE IF EXISTS knowledge_folders;
