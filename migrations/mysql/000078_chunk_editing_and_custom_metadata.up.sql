-- Migration 000078: editable chunks, revision history, and custom metadata.
ALTER TABLE chunks
    ADD COLUMN source_content TEXT NOT NULL DEFAULT (''),
    ADD COLUMN content_revision INT NOT NULL DEFAULT 0,
    ADD COLUMN index_status VARCHAR(16) NOT NULL DEFAULT 'ready',
    ADD COLUMN last_editor_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN context_header TEXT NOT NULL DEFAULT ('');

UPDATE chunks SET source_content = content WHERE source_content = '';

ALTER TABLE knowledges
    ADD COLUMN custom_metadata JSON NOT NULL DEFAULT (JSON_OBJECT());

CREATE TABLE chunk_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    revision INT NOT NULL,
    content TEXT NOT NULL DEFAULT (''),
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    editor_id VARCHAR(64) NOT NULL DEFAULT '',
    edit_source VARCHAR(16) NOT NULL DEFAULT 'user',
    edited_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_chunk_revisions_chunk_revision (chunk_id, revision),
    KEY idx_chunk_revisions_tenant_chunk (tenant_id, chunk_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
