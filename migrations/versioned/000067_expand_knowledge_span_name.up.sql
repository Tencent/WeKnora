-- Migration: 000067_expand_knowledge_span_name
--
-- Wiki/Graph subspan names can include deterministic page slugs or chunk
-- labels. Keep the trace row stable instead of failing writes when the
-- diagnostic name is longer than the original 64-byte budget.

DO $$ BEGIN RAISE NOTICE '[Migration 000067] Expanding knowledge_processing_spans.name'; END $$;

ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(255);

DO $$ BEGIN RAISE NOTICE '[Migration 000067] knowledge_processing_spans.name expanded'; END $$;
