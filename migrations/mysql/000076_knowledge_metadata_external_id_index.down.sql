ALTER TABLE knowledges
    DROP INDEX idx_knowledges_kb_metadata_external_id,
    DROP COLUMN metadata_external_id;
