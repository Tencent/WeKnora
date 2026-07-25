-- MySQL 8 translation of 000023_im_channel_kb_id.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE im_channels ADD COLUMN knowledge_base_id VARCHAR(36) DEFAULT '';
