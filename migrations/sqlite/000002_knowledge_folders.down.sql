-- Migration: 000002_knowledge_folders (down)

DROP INDEX IF EXISTS idx_knowledges_kb_folder;

-- Requires SQLite >= 3.35 (2021-03); mattn/go-sqlite3 bundles a newer build.
ALTER TABLE knowledges
    DROP COLUMN folder_id;

DROP INDEX IF EXISTS idx_knowledge_folders_deleted_at;
DROP INDEX IF EXISTS idx_knowledge_folders_parent;
DROP INDEX IF EXISTS idx_knowledge_folders_parent_name;

DROP TABLE IF EXISTS knowledge_folders;
