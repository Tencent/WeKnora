-- Migration: 000075_knowledge_folders (down)

DROP TRIGGER IF EXISTS trg_knowledges_folder_scope_update;
DROP TRIGGER IF EXISTS trg_knowledges_folder_scope_insert;

DROP INDEX IF EXISTS idx_knowledges_folder;

ALTER TABLE knowledges
    DROP COLUMN folder_id;

DROP TABLE IF EXISTS knowledge_folders;

DROP INDEX IF EXISTS uq_knowledge_bases_tenant_id_id;
