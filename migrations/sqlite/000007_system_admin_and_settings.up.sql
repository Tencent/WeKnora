-- Mirrors versioned migration 000053_system_admin_and_settings:
-- users.is_system_admin + system_settings table.

ALTER TABLE users ADD COLUMN is_system_admin BOOLEAN NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_users_is_system_admin ON users (is_system_admin);

CREATE TABLE IF NOT EXISTS system_settings (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    key              VARCHAR(128) NOT NULL UNIQUE,
    value            TEXT NOT NULL,
    value_type       VARCHAR(16)  NOT NULL,
    category         VARCHAR(32)  NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    is_secret        BOOLEAN NOT NULL DEFAULT 0,
    requires_restart BOOLEAN NOT NULL DEFAULT 0,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_system_settings_category
    ON system_settings (category);
