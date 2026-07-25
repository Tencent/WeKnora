-- MySQL 8 translation of 000025_message_channel.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN channel VARCHAR(50) NOT NULL DEFAULT '';
ALTER TABLE knowledges ADD COLUMN channel VARCHAR(50) NOT NULL DEFAULT 'web';
