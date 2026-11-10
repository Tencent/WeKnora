DO $$ BEGIN RAISE NOTICE '[Migration 000026] Dropping chunks query indexes'; END $$;

DROP INDEX IF EXISTS idx_chunks_kb_tenant;
DROP INDEX IF EXISTS idx_chunks_knowledge_enabled;
