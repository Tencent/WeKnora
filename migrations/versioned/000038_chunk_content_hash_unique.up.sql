-- Migration: 000038_chunk_content_hash_unique
-- Description: Add a unique index on (knowledge_id, content_hash) for chunks
-- to support idempotent INSERT of content-addressed chunk IDs (PR-1 step 1-6).
-- content_hash may be NULL for pre-existing non-FAQ chunks; the partial index
-- only enforces uniqueness when content_hash IS NOT NULL.
DO $$ BEGIN RAISE NOTICE '[Migration 000038] Applying chunk content_hash unique index'; END $$;

-- Partial unique index: only enforce when content_hash is populated.
-- This allows legacy chunks with NULL content_hash to coexist.
CREATE UNIQUE INDEX IF NOT EXISTS uk_chunks_knowledge_content_hash
    ON chunks (knowledge_id, content_hash)
    WHERE content_hash IS NOT NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000038] chunk content_hash unique index applied successfully'; END $$;