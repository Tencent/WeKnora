DO $$ BEGIN RAISE NOTICE '[Migration 000076] Dropping knowledge metadata external_id prefix index...'; END $$;

DROP INDEX IF EXISTS idx_knowledges_kb_metadata_external_id;
