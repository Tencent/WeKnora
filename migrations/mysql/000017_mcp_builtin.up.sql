-- MySQL 8 translation of 000017_mcp_builtin.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE mcp_services ADD COLUMN is_builtin TINYINT(1) NOT NULL DEFAULT 0;
CREATE INDEX idx_mcp_services_is_builtin ON mcp_services(is_builtin);
