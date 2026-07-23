-- Migration: 000075_add_folders

CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_bases_tenant_id_id
    ON knowledge_bases (tenant_id, id);

CREATE TABLE folders (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    parent_id         VARCHAR(36),
    name              VARCHAR(255) NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        DATETIME,
    UNIQUE (tenant_id, knowledge_base_id, id),
    FOREIGN KEY (tenant_id, knowledge_base_id)
        REFERENCES knowledge_bases (tenant_id, id)
        ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, knowledge_base_id, parent_id)
        REFERENCES folders (tenant_id, knowledge_base_id, id)
        ON DELETE NO ACTION
);

CREATE INDEX idx_folders_scope_parent
    ON folders (tenant_id, knowledge_base_id, parent_id);

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

-- SQLite cannot add a composite foreign key to an existing table without
-- rebuilding it. These triggers preserve the existing table and enforce the
-- same tenant/knowledge-base scoped folder reference as PostgreSQL.
CREATE TRIGGER trg_knowledges_folder_scope_insert
BEFORE INSERT ON knowledges
FOR EACH ROW
WHEN NEW.folder_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM folders
    WHERE id = NEW.folder_id
      AND tenant_id = NEW.tenant_id
      AND knowledge_base_id = NEW.knowledge_base_id
)
BEGIN
    SELECT RAISE(ABORT, 'invalid knowledge folder');
END;

CREATE TRIGGER trg_knowledges_folder_scope_update
BEFORE UPDATE OF tenant_id, knowledge_base_id, folder_id ON knowledges
FOR EACH ROW
WHEN NEW.folder_id IS NOT NULL AND NOT EXISTS (
    SELECT 1
    FROM folders
    WHERE id = NEW.folder_id
      AND tenant_id = NEW.tenant_id
      AND knowledge_base_id = NEW.knowledge_base_id
)
BEGIN
    SELECT RAISE(ABORT, 'invalid knowledge folder');
END;

CREATE TRIGGER trg_folders_referenced_by_knowledge_delete
BEFORE DELETE ON folders
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM knowledges
    WHERE folder_id = OLD.id
      AND tenant_id = OLD.tenant_id
      AND knowledge_base_id = OLD.knowledge_base_id
)
BEGIN
    SELECT RAISE(ABORT, 'folder is referenced by knowledge');
END;

CREATE TRIGGER trg_folders_referenced_by_knowledge_update
BEFORE UPDATE OF id, tenant_id, knowledge_base_id ON folders
FOR EACH ROW
WHEN EXISTS (
    SELECT 1
    FROM knowledges
    WHERE folder_id = OLD.id
      AND tenant_id = OLD.tenant_id
      AND knowledge_base_id = OLD.knowledge_base_id
)
BEGIN
    SELECT RAISE(ABORT, 'folder is referenced by knowledge');
END;
