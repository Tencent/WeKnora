-- MySQL 8 translation of 000009_add_last_faq_import_result.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE knowledges ADD COLUMN last_faq_import_result JSON;
