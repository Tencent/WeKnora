DO $$ BEGIN RAISE NOTICE '[Migration 000078] Adding knowledge folders...'; END $$;

-- Multi-level folder tree for document knowledge bases (issue #1311).
-- Mirrors the wiki_folders design (000061): adjacency list via parent_id
-- ('' = root), with a materialized "/"-joined path kept purely for cheap
-- display/sort. A document's placement is knowledges.folder_id.
CREATE TABLE IF NOT EXISTS knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(1024) NOT NULL DEFAULT '',
    depth             INT NOT NULL DEFAULT 0,
    sort_order        INT NOT NULL DEFAULT 0,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMP WITH TIME ZONE
);

-- A folder name is unique among its live siblings under the same parent.
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_folders_parent_name
    ON knowledge_folders (knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent
    ON knowledge_folders (knowledge_base_id, parent_id);

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at
    ON knowledge_folders (deleted_at);

-- knowledges.folder_id references knowledge_folders.id ('' = KB root).
-- Existing rows stay at the root; documents previously uploaded with a
-- relative path in file_name can be organized later via the idempotent
-- "organize by upload path" API — no silent data migration here.
ALTER TABLE knowledges
    ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_knowledges_kb_folder
    ON knowledges (knowledge_base_id, folder_id)
    WHERE deleted_at IS NULL;
