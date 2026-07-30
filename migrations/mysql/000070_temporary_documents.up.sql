CREATE TABLE temporary_documents (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref TEXT NOT NULL,
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'uploaded',
    content LONGTEXT NOT NULL DEFAULT (''),
    chunks JSON NOT NULL DEFAULT (JSON_ARRAY()),
    image_refs JSON NOT NULL DEFAULT (JSON_ARRAY()),
    metadata JSON NOT NULL DEFAULT (JSON_OBJECT()),
    processing_options JSON NOT NULL DEFAULT (JSON_OBJECT()),
    token_count INT NOT NULL DEFAULT 0,
    chunk_count INT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT (''),
    expires_at DATETIME(3) NOT NULL,
    started_at DATETIME(3) NULL,
    ready_at DATETIME(3) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    deleted_at DATETIME(3) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_temporary_documents_scope
    ON temporary_documents(tenant_id, session_id);
CREATE INDEX idx_temporary_documents_status
    ON temporary_documents(status);
CREATE INDEX idx_temporary_documents_expires
    ON temporary_documents(expires_at);
