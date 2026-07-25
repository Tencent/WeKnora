-- MySQL 8 translation of 000027_message_rendered_content.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN rendered_content TEXT NOT NULL;
