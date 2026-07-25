ALTER TABLE knowledges
    ADD COLUMN metadata_external_id VARCHAR(2048)
        GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(metadata, '$."external_id"'))) STORED;

CREATE INDEX idx_knowledges_kb_metadata_external_id
    ON knowledges(knowledge_base_id, deleted_at, metadata_external_id(191));
