-- MySQL 8 translation of 000035_add_credentials.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE tenants ADD COLUMN credentials JSON;
