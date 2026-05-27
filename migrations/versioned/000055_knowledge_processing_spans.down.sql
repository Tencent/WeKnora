-- Migration: 000054_knowledge_processing_spans (rollback)
DO $$ BEGIN RAISE NOTICE '[Migration 000054 rollback] Dropping table: knowledge_processing_spans'; END $$;

DROP INDEX IF EXISTS idx_kpspan_parent;
DROP INDEX IF EXISTS idx_kpspan_status_started;
DROP INDEX IF EXISTS idx_kpspan_knowledge_attempt;
DROP TABLE IF EXISTS knowledge_processing_spans;

DO $$ BEGIN RAISE NOTICE '[Migration 000054 rollback] complete'; END $$;
