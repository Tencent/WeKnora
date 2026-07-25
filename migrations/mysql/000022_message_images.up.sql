-- MySQL 8 translation of 000022_message_images.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN images JSON;
