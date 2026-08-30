-- Folder tree support for knowledge bases.
ALTER TABLE knowledges
    ADD COLUMN folder_path VARCHAR(1024) NOT NULL DEFAULT '';

-- Preserve legacy folder uploads while separating the base file name.
UPDATE knowledges
SET folder_path = LEFT(LEFT(file_name, CHAR_LENGTH(file_name) - LOCATE('/', REVERSE(file_name))), 1024),
    file_name = SUBSTRING_INDEX(file_name, '/', -1) WHERE file_name LIKE '%/%';

CREATE INDEX idx_knowledges_folder_path
    ON knowledges (tenant_id, knowledge_base_id, folder_path);
