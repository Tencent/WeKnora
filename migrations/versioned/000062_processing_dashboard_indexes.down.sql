-- Migration: 000062_processing_dashboard_indexes (rollback)

DO $$ BEGIN RAISE NOTICE '[Migration 000062 rollback] Dropping processing dashboard indexes'; END $$;

DROP INDEX IF EXISTS idx_task_pending_ops_dashboard;
DROP INDEX IF EXISTS idx_knowledge_dashboard_active;
DROP INDEX IF EXISTS idx_kps_status_updated;
DROP INDEX IF EXISTS idx_kps_knowledge_attempt_name_id;
DROP INDEX IF EXISTS idx_kps_knowledge_attempt;

DO $$ BEGIN RAISE NOTICE '[Migration 000062 rollback] complete'; END $$;
