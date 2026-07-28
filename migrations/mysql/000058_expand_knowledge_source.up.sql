-- MySQL 8 translation of 000058_expand_knowledge_source.
ALTER TABLE knowledges
    MODIFY COLUMN source VARCHAR(2048) NOT NULL;
