-- MySQL 8 translation of 000014_storage_provider_config.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE knowledge_bases ADD COLUMN storage_provider_config JSON;
