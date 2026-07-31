-- Migration: 000079_document_folders (down)
-- Reverses the document-folders schema. knowledges.folder_id is dropped last
-- so any late reference is cleared after the table is gone.

DROP INDEX IF EXISTS idx_knowledges_folder;
ALTER TABLE knowledges DROP COLUMN IF EXISTS folder_id;

DROP INDEX IF EXISTS idx_doc_folders_deleted_at;
DROP INDEX IF EXISTS idx_doc_folders_scope_parent;
DROP INDEX IF EXISTS idx_doc_folders_parent_name;
DROP TABLE IF EXISTS document_folders;
