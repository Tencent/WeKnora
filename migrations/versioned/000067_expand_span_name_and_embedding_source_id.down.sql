-- Rollback narrows values explicitly so existing long rows do not make the
-- schema rollback fail. This is intentionally lossy and only for rollback.

ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(64) USING LEFT(name, 64);

ALTER TABLE IF EXISTS embeddings
    ALTER COLUMN source_id TYPE VARCHAR(64) USING LEFT(source_id, 64);
