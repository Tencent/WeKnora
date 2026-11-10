CREATE TABLE IF NOT EXISTS temporary_documents (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    resource_ref TEXT NOT NULL,
    file_name VARCHAR(1024) NOT NULL,
    file_type VARCHAR(32) NOT NULL,
    mime_type VARCHAR(255) NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'uploaded',
    content TEXT NOT NULL DEFAULT '',
    chunks JSONB NOT NULL DEFAULT '[]'::jsonb,
    image_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    processing_options JSONB NOT NULL DEFAULT '{}'::jsonb,
    token_count INTEGER NOT NULL DEFAULT 0,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP NULL,
    ready_at TIMESTAMP NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);

CREATE INDEX IF NOT EXISTS idx_temporary_documents_scope ON temporary_documents(tenant_id, session_id);
CREATE INDEX IF NOT EXISTS idx_temporary_documents_status ON temporary_documents(status);
CREATE INDEX IF NOT EXISTS idx_temporary_documents_expires ON temporary_documents(expires_at);
