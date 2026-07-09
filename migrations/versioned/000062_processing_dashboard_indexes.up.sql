-- Migration: 000062_processing_dashboard_indexes
-- Description: Add read-only query indexes for the knowledge processing dashboard.

DO $$ BEGIN RAISE NOTICE '[Migration 000062] Adding processing dashboard indexes'; END $$;

CREATE INDEX IF NOT EXISTS idx_kps_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);

CREATE INDEX IF NOT EXISTS idx_kps_knowledge_attempt_name_id
    ON knowledge_processing_spans (knowledge_id, attempt, name, id);

CREATE INDEX IF NOT EXISTS idx_kps_status_updated
    ON knowledge_processing_spans (status, updated_at);

CREATE INDEX IF NOT EXISTS idx_knowledge_dashboard_active
    ON knowledges (tenant_id, parse_status, knowledge_base_id, updated_at);

CREATE INDEX IF NOT EXISTS idx_task_pending_ops_dashboard
    ON task_pending_ops (tenant_id, task_type, dedup_key, fail_count, id);

DO $$ BEGIN RAISE NOTICE '[Migration 000062] processing dashboard indexes ready'; END $$;
