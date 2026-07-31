-- Migration: 000002_knowledge_folder_index_pending

CREATE TABLE IF NOT EXISTS knowledge_folder_index_pending (
    id                VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL,
    target_folder_id  VARCHAR(36) NOT NULL DEFAULT '',
    requested_version INTEGER NOT NULL CHECK (requested_version > 0),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_knowledge_folder_index_pending_scope
        UNIQUE (tenant_id, knowledge_base_id, knowledge_id)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_folder_index_pending_scope_updated
    ON knowledge_folder_index_pending (
        tenant_id,
        knowledge_base_id,
        updated_at,
        knowledge_id
    );
