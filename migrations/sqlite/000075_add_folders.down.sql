-- Migration: 000075_add_folders (down)

DROP TRIGGER IF EXISTS trg_folders_referenced_by_knowledge_update;
DROP TRIGGER IF EXISTS trg_folders_referenced_by_knowledge_delete;
DROP TRIGGER IF EXISTS trg_knowledges_folder_scope_update;
DROP TRIGGER IF EXISTS trg_knowledges_folder_scope_insert;

DROP INDEX IF EXISTS idx_knowledges_scope_folder;

ALTER TABLE knowledges
    DROP COLUMN folder_id;

DELETE FROM folders;
DROP TABLE IF EXISTS folders;

DROP INDEX IF EXISTS uq_knowledge_bases_tenant_id_id;
