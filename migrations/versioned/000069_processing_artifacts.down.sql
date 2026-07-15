DO $$ BEGIN RAISE NOTICE '[Migration 000066 down] Dropping processing_artifacts...'; END $$;

DROP INDEX IF EXISTS idx_processing_artifacts_tenant_created;
DROP TABLE IF EXISTS processing_artifacts;

DO $$ BEGIN RAISE NOTICE '[Migration 000066 down] processing_artifacts dropped'; END $$;
