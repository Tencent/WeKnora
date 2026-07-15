-- Remove folder metadata without deleting knowledge rows.
DROP INDEX IF EXISTS idx_knowledges_folder;
ALTER TABLE knowledges DROP COLUMN IF EXISTS folder_id;
DROP TABLE IF EXISTS knowledge_folder_closure;
DROP TABLE IF EXISTS knowledge_folders;
