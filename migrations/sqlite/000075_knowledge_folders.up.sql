-- Migration: 000075_knowledge_folders

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
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME,
    UNIQUE (tenant_id, knowledge_base_id, id),
    FOREIGN KEY (tenant_id, knowledge_base_id)
        REFERENCES knowledge_bases (tenant_id, id)
        ON DELETE CASCADE,
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

CREATE TRIGGER trg_knowledges_folder_scope_insert
BEFORE INSERT ON knowledges
FOR EACH ROW
WHEN NEW.folder_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM knowledge_folders
    WHERE id = NEW.folder_id
      AND tenant_id = NEW.tenant_id
      AND knowledge_base_id = NEW.knowledge_base_id
      AND deleted_at IS NULL
)
BEGIN
    SELECT RAISE(ABORT, 'invalid knowledge folder');
END;

CREATE TRIGGER trg_knowledges_folder_scope_update
BEFORE UPDATE OF tenant_id, knowledge_base_id, folder_id ON knowledges
FOR EACH ROW
WHEN NEW.folder_id IS NOT NULL AND NOT EXISTS (
    SELECT 1 FROM knowledge_folders
    WHERE id = NEW.folder_id
      AND tenant_id = NEW.tenant_id
      AND knowledge_base_id = NEW.knowledge_base_id
      AND deleted_at IS NULL
)
BEGIN
    SELECT RAISE(ABORT, 'invalid knowledge folder');
END;
