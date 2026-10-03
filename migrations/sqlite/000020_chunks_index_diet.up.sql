-- Drop chunks indexes that only slow down writes (see versioned/000100).
DROP INDEX IF EXISTS idx_chunks_chunk_type;
DROP INDEX IF EXISTS idx_chunks_content_hash;
DROP INDEX IF EXISTS idx_chunks_tenant_kg;
