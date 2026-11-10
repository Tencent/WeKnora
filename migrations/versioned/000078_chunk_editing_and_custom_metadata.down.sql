-- Roll back migration 000078.
DROP TABLE IF EXISTS chunk_revisions;
ALTER TABLE knowledges DROP COLUMN IF EXISTS custom_metadata;
ALTER TABLE chunks DROP COLUMN IF EXISTS context_header;
ALTER TABLE chunks DROP COLUMN IF EXISTS last_editor_id;
ALTER TABLE chunks DROP COLUMN IF EXISTS index_status;
ALTER TABLE chunks DROP COLUMN IF EXISTS content_revision;
ALTER TABLE chunks DROP COLUMN IF EXISTS source_content;
