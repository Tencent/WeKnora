ALTER TABLE knowledge_processing_spans
    ALTER COLUMN name TYPE VARCHAR(64) USING LEFT(name, 64);
