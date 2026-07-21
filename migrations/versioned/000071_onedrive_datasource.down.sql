DROP TABLE IF EXISTS data_source_items;
DROP TABLE IF EXISTS data_source_oauth_tokens;
ALTER TABLE data_sources DROP COLUMN IF EXISTS connection_version;
