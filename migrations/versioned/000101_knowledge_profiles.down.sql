ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS generated_profile;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS profile_config;
ALTER TABLE knowledges DROP COLUMN IF EXISTS profile;
