ALTER TABLE chunks
    ADD COLUMN source_content LONGTEXT NULL,
    ADD COLUMN content_revision INT NOT NULL DEFAULT 0,
    ADD COLUMN index_status VARCHAR(16) NOT NULL DEFAULT 'ready',
    ADD COLUMN last_editor_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN context_header TEXT NULL;

UPDATE chunks SET source_content = content WHERE source_content IS NULL OR source_content = '';
UPDATE chunks SET context_header = '' WHERE context_header IS NULL;

ALTER TABLE chunks
    MODIFY COLUMN source_content LONGTEXT NOT NULL DEFAULT (''),
    MODIFY COLUMN context_header TEXT NOT NULL DEFAULT ('');

ALTER TABLE knowledges
    ADD COLUMN custom_metadata JSON NOT NULL DEFAULT (JSON_OBJECT());

CREATE TABLE IF NOT EXISTS chunk_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    revision INT NOT NULL,
    content LONGTEXT NOT NULL DEFAULT (''),
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    editor_id VARCHAR(64) NOT NULL DEFAULT '',
    edit_source VARCHAR(16) NOT NULL DEFAULT 'user',
    edited_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY idx_chunk_revisions_chunk_revision (chunk_id, revision),
    KEY idx_chunk_revisions_tenant_chunk (tenant_id, chunk_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
