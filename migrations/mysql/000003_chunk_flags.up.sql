-- MySQL 8 translation of 000003_chunk_flags.up.sql.
ALTER TABLE chunks
    ADD COLUMN flags INTEGER NOT NULL DEFAULT 1;
