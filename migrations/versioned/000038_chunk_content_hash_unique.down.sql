-- Migration: 000038_chunk_content_hash_unique (rollback)
-- Description: Drop the unique index on (knowledge_id, content_hash).
DO $$ BEGIN RAISE NOTICE '[Migration 000038 DOWN] Reverting chunk content_hash unique index'; END $$;

DROP INDEX IF EXISTS uk_chunks_knowledge_content_hash;

DO $$ BEGIN RAISE NOTICE '[Migration 000038 DOWN] chunk content_hash unique index reverted successfully'; END $$;