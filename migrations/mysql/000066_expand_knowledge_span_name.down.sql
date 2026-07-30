UPDATE knowledge_processing_spans
SET name = LEFT(name, 64)
WHERE CHAR_LENGTH(name) > 64;

ALTER TABLE knowledge_processing_spans
    MODIFY COLUMN name VARCHAR(64) NOT NULL;
