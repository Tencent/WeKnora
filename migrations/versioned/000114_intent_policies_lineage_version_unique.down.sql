-- Migration: 000114_intent_policies_lineage_version_unique (rollback)
DO $$ BEGIN RAISE NOTICE '[Migration 000114] Dropping unique index idx_intent_policies_lineage_version'; END $$;

DROP INDEX IF EXISTS idx_intent_policies_lineage_version;
