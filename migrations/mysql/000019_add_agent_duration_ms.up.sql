-- MySQL 8 translation of 000019_add_agent_duration_ms.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE messages ADD COLUMN agent_duration_ms BIGINT DEFAULT 0;
