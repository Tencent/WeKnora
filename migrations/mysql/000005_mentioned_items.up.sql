-- MySQL 8 translation of 000005_mentioned_items.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN mentioned_items JSON;
