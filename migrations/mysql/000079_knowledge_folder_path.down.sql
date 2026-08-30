UPDATE knowledges
SET file_name = CONCAT(folder_path, '/', file_name) WHERE folder_path <> '' AND file_name <> '';

DROP INDEX idx_knowledges_folder_path ON knowledges;

ALTER TABLE knowledges DROP COLUMN folder_path;
