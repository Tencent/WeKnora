DROP TABLE IF EXISTS knowledge_file_versions;
ALTER TABLE knowledges DROP COLUMN IF EXISTS file_version_created_at;
ALTER TABLE knowledges DROP COLUMN IF EXISTS file_version;
