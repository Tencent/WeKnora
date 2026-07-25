-- MySQL 8 translation of 000015_add_is_fallback.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN is_fallback TINYINT(1) DEFAULT 0;
