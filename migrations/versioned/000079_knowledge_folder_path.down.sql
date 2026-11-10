-- Restore the legacy representation: the relative directory is folded back
-- into file_name so a rollback keeps folder uploads distinguishable.
UPDATE knowledges
SET file_name = folder_path || '/' || file_name
WHERE folder_path <> '' AND file_name <> '';

DROP INDEX IF EXISTS idx_knowledges_folder_path;

ALTER TABLE knowledges DROP COLUMN IF EXISTS folder_path;
