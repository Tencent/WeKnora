-- MySQL 8 translation of 000070_temporary_documents.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

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
    content TEXT NOT NULL,
    chunks JSON NOT NULL,
    image_refs JSON NOT NULL,
    metadata JSON NOT NULL,
    processing_options JSON NOT NULL,
    token_count INTEGER NOT NULL DEFAULT 0,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP NULL,
    ready_at TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);
CREATE INDEX idx_temporary_documents_scope ON temporary_documents(tenant_id, session_id);
CREATE INDEX idx_temporary_documents_status ON temporary_documents(status);
CREATE INDEX idx_temporary_documents_expires ON temporary_documents(expires_at);
