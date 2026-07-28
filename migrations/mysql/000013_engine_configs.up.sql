-- MySQL 8 translation of 000013_engine_configs.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE tenants ADD COLUMN parser_engine_config JSON;
ALTER TABLE tenants ADD COLUMN storage_engine_config JSON;
