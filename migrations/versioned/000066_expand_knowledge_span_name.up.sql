DO $$ BEGIN RAISE NOTICE '[Migration 000066] Expanding knowledge_processing_spans.name to VARCHAR(255)...'; END $$;

ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(255);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] knowledge_processing_spans.name expanded'; END $$;
