-- Migration: 000002_document_folders (down)

DROP INDEX IF EXISTS idx_lite_embeddings_folder_id;
ALTER TABLE lite_embeddings DROP COLUMN folder_id;

DROP INDEX IF EXISTS idx_knowledges_folder;
ALTER TABLE knowledges DROP COLUMN folder_id;

DROP INDEX IF EXISTS idx_doc_folders_deleted_at;
DROP INDEX IF EXISTS idx_doc_folders_scope_parent;
DROP INDEX IF EXISTS idx_doc_folders_parent_name;
DROP TABLE IF EXISTS document_folders;
