TRUNCATE TABLE embedding_cache_entries;

ALTER TABLE embedding_cache_entries
    ADD COLUMN checksum_sha256 CHAR(64) NOT NULL;
