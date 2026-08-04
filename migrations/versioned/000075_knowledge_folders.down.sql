-- Migration: 000075_knowledge_folders (down)

ALTER TABLE knowledges
    DROP CONSTRAINT IF EXISTS fk_knowledges_knowledge_folder;

DROP INDEX IF EXISTS idx_knowledges_folder;

ALTER TABLE knowledges
    DROP COLUMN IF EXISTS folder_id;

DROP TABLE IF EXISTS knowledge_folders;

DROP INDEX IF EXISTS uq_knowledge_bases_tenant_id_id;
