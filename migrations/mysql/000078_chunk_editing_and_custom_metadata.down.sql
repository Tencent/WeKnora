DROP TABLE IF EXISTS chunk_revisions;

ALTER TABLE knowledges
    DROP COLUMN custom_metadata;

ALTER TABLE chunks
    DROP COLUMN context_header,
    DROP COLUMN last_editor_id,
    DROP COLUMN index_status,
    DROP COLUMN content_revision,
    DROP COLUMN source_content;
