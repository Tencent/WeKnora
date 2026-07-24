DO $$ BEGIN RAISE NOTICE '[Migration 000066 down] Reverting knowledge_processing_spans.name to VARCHAR(64)...'; END $$;

ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(64) USING LEFT(name, 64);

DO $$ BEGIN RAISE NOTICE '[Migration 000066 down] knowledge_processing_spans.name reverted'; END $$;
