-- MySQL 8 translation of 000066_expand_knowledge_span_name.
ALTER TABLE knowledge_processing_spans MODIFY COLUMN name VARCHAR(255) NOT NULL;
