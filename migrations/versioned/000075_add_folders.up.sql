-- Migration: 000075_add_folders
-- Description: Add adjacency-list folders for document knowledge bases.

CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_bases_tenant_id_id
    ON knowledge_bases (tenant_id, id);

CREATE TABLE folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36),
    name              VARCHAR(255) NOT NULL,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        TIMESTAMP WITH TIME ZONE,
    CONSTRAINT uq_folders_scope_id UNIQUE (tenant_id, knowledge_base_id, id),
    CONSTRAINT fk_folders_knowledge_base
        FOREIGN KEY (tenant_id, knowledge_base_id)
        REFERENCES knowledge_bases (tenant_id, id)
        ON DELETE CASCADE,
    CONSTRAINT fk_folders_parent
        FOREIGN KEY (tenant_id, knowledge_base_id, parent_id)
        REFERENCES folders (tenant_id, knowledge_base_id, id)
        ON DELETE NO ACTION
);

CREATE INDEX idx_folders_scope_parent
    ON folders (tenant_id, knowledge_base_id, parent_id);

-- PostgreSQL treats NULL values as distinct in an ordinary unique index, so
-- root and non-root siblings need separate live-row constraints.
CREATE UNIQUE INDEX uq_folders_live_root_name
    ON folders (tenant_id, knowledge_base_id, name)
    WHERE parent_id IS NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_folders_live_sibling_name
    ON folders (tenant_id, knowledge_base_id, parent_id, name)
    WHERE parent_id IS NOT NULL AND deleted_at IS NULL;

ALTER TABLE knowledges
    ADD COLUMN folder_id VARCHAR(36);

CREATE INDEX idx_knowledges_scope_folder
    ON knowledges (tenant_id, knowledge_base_id, folder_id);

ALTER TABLE knowledges
    ADD CONSTRAINT fk_knowledges_folder
    FOREIGN KEY (tenant_id, knowledge_base_id, folder_id)
    REFERENCES folders (tenant_id, knowledge_base_id, id)
    ON DELETE NO ACTION;
