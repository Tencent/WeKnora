-- MySQL 8 translation of 000024_im_channel_bot_identity.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE im_channels ADD COLUMN bot_identity VARCHAR(255) NOT NULL DEFAULT '';
