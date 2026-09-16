DELETE FROM embedding_cache_entries;

ALTER TABLE embedding_cache_entries
    ADD COLUMN checksum_sha256 TEXT NOT NULL DEFAULT '';
