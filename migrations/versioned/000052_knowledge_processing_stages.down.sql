-- Migration: 000052_knowledge_processing_stages (rollback)
DO $$ BEGIN RAISE NOTICE '[Migration 000052 rollback] Dropping table: knowledge_processing_stages'; END $$;

DROP INDEX IF EXISTS idx_kps_status_started;
DROP INDEX IF EXISTS idx_kps_knowledge;
DROP TABLE IF EXISTS knowledge_processing_stages;

DO $$ BEGIN RAISE NOTICE '[Migration 000052 rollback] complete'; END $$;
