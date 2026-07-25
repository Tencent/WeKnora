-- MySQL 8 translation of 000053_system_admin_and_settings.up.sql.
-- PostgreSQL-only procedural/data steps are intentionally omitted.

ALTER TABLE users
    ADD COLUMN is_system_admin TINYINT(1) NOT NULL DEFAULT 0;
CREATE INDEX idx_users_is_system_admin ON users (is_system_admin);
CREATE TABLE system_settings (
    id               BIGINT AUTO_INCREMENT PRIMARY KEY,
    `key`            VARCHAR(128) NOT NULL UNIQUE,
    value            JSON NOT NULL,
    value_type       VARCHAR(16)  NOT NULL,
    category         VARCHAR(32)  NOT NULL,
    description      TEXT NOT NULL,
    is_secret        TINYINT(1) NOT NULL DEFAULT 0,
    requires_restart TINYINT(1) NOT NULL DEFAULT 0,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_system_settings_category
    ON system_settings (category);
