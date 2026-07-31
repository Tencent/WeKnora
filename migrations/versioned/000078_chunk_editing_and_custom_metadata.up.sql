-- Migration 000078: editable chunks, revision history, and custom document metadata.
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS source_content TEXT NOT NULL DEFAULT '';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS content_revision INT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS index_status VARCHAR(16) NOT NULL DEFAULT 'ready';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS last_editor_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS context_header TEXT NOT NULL DEFAULT '';
UPDATE chunks SET source_content = content WHERE source_content = '';

ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS custom_metadata JSONB NOT NULL DEFAULT '{}'::JSONB;

CREATE TABLE IF NOT EXISTS chunk_revisions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    revision INT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    editor_id VARCHAR(64) NOT NULL DEFAULT '',
    edit_source VARCHAR(16) NOT NULL DEFAULT 'user',
    edited_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_chunk_revisions_chunk_revision ON chunk_revisions (chunk_id, revision);
CREATE INDEX IF NOT EXISTS idx_chunk_revisions_tenant_chunk ON chunk_revisions (tenant_id, chunk_id);
