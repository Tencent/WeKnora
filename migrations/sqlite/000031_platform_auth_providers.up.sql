-- SQLite mirror of 000111_platform_auth_providers.up.sql (Lite edition).
-- See the Postgres migration for the design notes.

CREATE TABLE IF NOT EXISTS platform_auth_providers (
    id         INTEGER      PRIMARY KEY AUTOINCREMENT,
    kind       VARCHAR(16)  NOT NULL,
    enabled    INTEGER      NOT NULL DEFAULT 0,
    config     TEXT         NOT NULL DEFAULT '{}',
    secret     TEXT         NOT NULL DEFAULT '',
    updated_by VARCHAR(36)  NOT NULL DEFAULT '',
    created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_auth_providers_kind
    ON platform_auth_providers (kind);

ALTER TABLE users ADD COLUMN auth_source VARCHAR(16) NOT NULL DEFAULT 'local';
