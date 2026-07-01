-- Migration: 000067_expand_knowledge_span_name (rollback)

DO $$ BEGIN RAISE NOTICE '[Migration 000067 rollback] Shrinking knowledge_processing_spans.name'; END $$;

ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(64);

DO $$ BEGIN RAISE NOTICE '[Migration 000067 rollback] knowledge_processing_spans.name restored'; END $$;
