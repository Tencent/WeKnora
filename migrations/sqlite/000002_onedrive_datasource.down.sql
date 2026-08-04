DROP TABLE IF EXISTS data_source_items;
DROP TABLE IF EXISTS data_source_oauth_tokens;
ALTER TABLE sync_logs DROP COLUMN checkpoint;
ALTER TABLE data_sources DROP COLUMN connection_version;
