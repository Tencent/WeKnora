-- MySQL 8 translation of 000016_add_kb_pinned.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE knowledge_bases ADD COLUMN is_pinned TINYINT(1) NOT NULL DEFAULT 0;
ALTER TABLE knowledge_bases ADD COLUMN pinned_at TIMESTAMP NULL;
