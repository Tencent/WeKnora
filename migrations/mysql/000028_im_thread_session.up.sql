-- MySQL 8 translation of 000028_im_thread_session.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE im_channels ADD COLUMN session_mode VARCHAR(20) NOT NULL DEFAULT 'user';
ALTER TABLE im_channel_sessions ADD COLUMN thread_id VARCHAR(128) NOT NULL DEFAULT '';
