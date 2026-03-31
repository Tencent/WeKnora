-- Rollback: 000028_datasource_tables
DROP TRIGGER IF EXISTS trg_sync_logs_updated_at ON sync_logs;
DROP TRIGGER IF EXISTS trg_data_sources_updated_at ON data_sources;

DROP TABLE IF EXISTS sync_logs;
DROP TABLE IF EXISTS data_sources;
