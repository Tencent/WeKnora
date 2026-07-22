-- Migration: 000071_knowledge_folders
-- Description: Add document knowledge folder hierarchy and index sync versions.

DO $$ BEGIN RAISE NOTICE '[Migration 000071] Applying knowledge folder schema'; END $$;

CREATE TABLE IF NOT EXISTS knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36) NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL CHECK (name <> ''),
    path              VARCHAR(2048) NOT NULL CHECK (path <> ''),
    depth             INTEGER NOT NULL CHECK (depth BETWEEN 1 AND 32),
    sort_order        INTEGER NOT NULL DEFAULT 0,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_folders_live_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_parent
    ON knowledge_folders (
        tenant_id,
        knowledge_base_id,
        parent_id,
        sort_order,
        name,
        created_at,
        id
    )
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_path
    ON knowledge_folders (tenant_id, knowledge_base_id, path varchar_pattern_ops)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_folders_deleted_at
    ON knowledge_folders (deleted_at);

ALTER TABLE knowledges
    ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS folder_version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS folder_indexed_version BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_knowledges_folder
    ON knowledges (tenant_id, knowledge_base_id, folder_id, id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_knowledges_folder_index_pending
    ON knowledges (tenant_id, knowledge_base_id, id)
    WHERE deleted_at IS NULL AND folder_indexed_version < folder_version;

DO $$ BEGIN RAISE NOTICE '[Migration 000071] Knowledge folder schema applied successfully'; END $$;
