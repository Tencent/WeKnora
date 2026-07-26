-- Migration: 000075_knowledge_folders
-- Description: Add hierarchical folders for document knowledge bases.

CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_bases_tenant_id_id
    ON knowledge_bases (tenant_id, id);

CREATE TABLE knowledge_folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36),
    name              VARCHAR(255) NOT NULL,
    path              VARCHAR(4096) NOT NULL,
    depth             INTEGER NOT NULL DEFAULT 1,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        TIMESTAMP WITH TIME ZONE,
    CONSTRAINT uq_knowledge_folders_scope_id
        UNIQUE (tenant_id, knowledge_base_id, id),
    CONSTRAINT fk_knowledge_folders_kb
        FOREIGN KEY (tenant_id, knowledge_base_id)
        REFERENCES knowledge_bases (tenant_id, id)
        ON DELETE CASCADE,
    CONSTRAINT fk_knowledge_folders_parent
        FOREIGN KEY (tenant_id, knowledge_base_id, parent_id)
        REFERENCES knowledge_folders (tenant_id, knowledge_base_id, id)
);

CREATE INDEX idx_knowledge_folders_parent
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_knowledge_folders_path
    ON knowledge_folders (tenant_id, knowledge_base_id, path)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX uq_knowledge_folders_root_name
    ON knowledge_folders (tenant_id, knowledge_base_id, name)
    WHERE parent_id IS NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_knowledge_folders_sibling_name
    ON knowledge_folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE parent_id IS NOT NULL AND deleted_at IS NULL;

ALTER TABLE knowledges
    ADD COLUMN folder_id VARCHAR(36);

CREATE INDEX idx_knowledges_folder
    ON knowledges (tenant_id, knowledge_base_id, folder_id)
    WHERE deleted_at IS NULL;

ALTER TABLE knowledges
    ADD CONSTRAINT fk_knowledges_knowledge_folder
    FOREIGN KEY (tenant_id, knowledge_base_id, folder_id)
    REFERENCES knowledge_folders (tenant_id, knowledge_base_id, id);
