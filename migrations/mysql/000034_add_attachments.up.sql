-- MySQL 8 translation of 000034_add_attachments.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN attachments JSON;
