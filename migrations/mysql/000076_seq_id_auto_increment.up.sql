-- Compatibility repair for databases initialized while 000010 translated
-- PostgreSQL sequences to a fixed zero default.
ALTER TABLE chunks MODIFY COLUMN seq_id BIGINT NOT NULL AUTO_INCREMENT;
ALTER TABLE chunks AUTO_INCREMENT = 100000000;
ALTER TABLE knowledge_tags MODIFY COLUMN seq_id BIGINT NOT NULL AUTO_INCREMENT;
ALTER TABLE knowledge_tags AUTO_INCREMENT = 10000000;
