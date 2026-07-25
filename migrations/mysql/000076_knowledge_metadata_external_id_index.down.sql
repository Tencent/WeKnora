DROP INDEX idx_knowledges_kb_metadata_external_id ON knowledges;

ALTER TABLE knowledges
    DROP COLUMN metadata_external_id;
