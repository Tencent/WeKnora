DO $$ BEGIN RAISE NOTICE '[Migration 000066] Widening knowledge processing span names'; END $$;

ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(256);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Knowledge processing span names widened'; END $$;
