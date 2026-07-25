-- MySQL 8 translation of 000018_extend_tenant_api_key.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE tenants MODIFY COLUMN api_key VARCHAR(256) NOT NULL;
