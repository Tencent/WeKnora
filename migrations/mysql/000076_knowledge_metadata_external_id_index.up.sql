-- Materialize metadata.external_id so MySQL can serve LIKE-prefix lookups from
-- a normal B-tree index. Binary collation preserves JSON string case semantics.
ALTER TABLE knowledges
    ADD COLUMN metadata_external_id VARCHAR(1024)
        COLLATE utf8mb4_bin
        GENERATED ALWAYS AS (
            JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.external_id'))
        ) STORED,
    ADD INDEX idx_knowledges_kb_metadata_external_id (
        knowledge_base_id,
        metadata_external_id(512)
    );
