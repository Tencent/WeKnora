-- MySQL 8 translation of 000031_add_asr_config.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE knowledge_bases ADD COLUMN asr_config JSON;
