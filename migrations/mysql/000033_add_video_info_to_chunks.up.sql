-- MySQL 8 translation of 000033_add_video_info_to_chunks.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE chunks ADD COLUMN video_info TEXT;
