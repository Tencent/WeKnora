-- Migration 000100 rollback: recreate the dropped chunks indexes.
CREATE INDEX IF NOT EXISTS idx_chunks_chunk_type ON chunks(chunk_type);
CREATE INDEX IF NOT EXISTS idx_chunks_content_hash ON chunks(content_hash);
CREATE INDEX IF NOT EXISTS idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);
