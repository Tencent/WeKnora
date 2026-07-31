-- Roll back migration 000079.
--
-- Dropping folder_id discards the filing layout but no documents: folders were
-- never an ownership relation, only a label on the document row.
DROP INDEX IF EXISTS idx_knowledges_kb_folder;
ALTER TABLE knowledges DROP COLUMN IF EXISTS folder_id;
DROP TABLE IF EXISTS knowledge_folders;
