-- Folder tree support for knowledge bases (SQLite / lite mode).
--
-- Mirrors migrations/versioned/000079_knowledge_folder_path.up.sql.
ALTER TABLE knowledges ADD COLUMN folder_path VARCHAR(1024) NOT NULL DEFAULT '';

-- Backfill legacy folder uploads, which encoded their relative directory in
-- file_name. SQLite has no regex, so the directory prefix is isolated with the
-- rtrim trick: trimming every non-slash character off the right leaves exactly
-- the prefix up to and including the last '/'.
UPDATE knowledges
SET folder_path = substr(
        rtrim(file_name, replace(file_name, '/', '')),
        1,
        length(rtrim(file_name, replace(file_name, '/', ''))) - 1
    ),
    file_name = substr(
        file_name,
        length(rtrim(file_name, replace(file_name, '/', ''))) + 1
    )
WHERE file_name LIKE '%/%';

CREATE INDEX IF NOT EXISTS idx_knowledges_folder_path
    ON knowledges (tenant_id, knowledge_base_id, folder_path);
