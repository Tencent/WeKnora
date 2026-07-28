-- MySQL 8 translation of 000010_add_seq_id.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

-- MySQL permits an AUTO_INCREMENT column that is not the primary key, as long
-- as it is indexed. This is the equivalent of the PostgreSQL sequences above.
ALTER TABLE chunks
    ADD COLUMN seq_id BIGINT NOT NULL AUTO_INCREMENT,
    ADD UNIQUE INDEX idx_chunks_seq_id (seq_id);
ALTER TABLE chunks AUTO_INCREMENT = 100000000;

ALTER TABLE knowledge_tags
    ADD COLUMN seq_id BIGINT NOT NULL AUTO_INCREMENT,
    ADD UNIQUE INDEX idx_knowledge_tags_seq_id (seq_id);
ALTER TABLE knowledge_tags AUTO_INCREMENT = 10000000;
