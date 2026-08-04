-- Folder tree support for knowledge bases.
--
-- Folder uploads used to encode their relative directory inside file_name
-- (e.g. "docs/spec/design.md"), which made the document list show the whole
-- path as the title and left no way to query a single folder. The path now
-- lives in its own column and file_name keeps only the base name.
ALTER TABLE knowledges
    ADD COLUMN IF NOT EXISTS folder_path VARCHAR(1024) NOT NULL DEFAULT '';

-- Backfill legacy folder uploads so existing knowledge bases render the same
-- tree as newly uploaded folders.
UPDATE knowledges
SET folder_path = LEFT(
        REGEXP_REPLACE(file_name, '/[^/]*$', ''),
        1024
    ),
    file_name = REGEXP_REPLACE(file_name, '^.*/', '')
WHERE file_name LIKE '%/%';

CREATE INDEX IF NOT EXISTS idx_knowledges_folder_path
    ON knowledges (tenant_id, knowledge_base_id, folder_path);
