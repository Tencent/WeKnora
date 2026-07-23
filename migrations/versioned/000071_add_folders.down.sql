-- Migration: 000071_add_folders (down)

ALTER TABLE knowledges
    DROP CONSTRAINT IF EXISTS fk_knowledges_folder;

DROP INDEX IF EXISTS idx_knowledges_scope_folder;

ALTER TABLE knowledges
    DROP COLUMN IF EXISTS folder_id;

DROP TABLE IF EXISTS folders;

DROP INDEX IF EXISTS uq_knowledge_bases_tenant_id_id;
