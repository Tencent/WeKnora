-- Rollback engine config columns
ALTER TABLE tenants DROP COLUMN IF EXISTS storage_engine_config;
ALTER TABLE tenants DROP COLUMN IF EXISTS parser_engine_config;
