-- Runtime settings and durable task queue schema for SQLite/Lite.
-- Mirrors the portions of versioned migrations 000041 and 000053 that are
-- consumed by the application in both PostgreSQL and SQLite deployments.

ALTER TABLE users ADD COLUMN is_system_admin BOOLEAN NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_users_is_system_admin ON users(is_system_admin);

ALTER TABLE knowledges ADD COLUMN pending_subtasks_count INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS system_settings (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    key              VARCHAR(128) NOT NULL UNIQUE,
    value            TEXT NOT NULL,
    value_type       VARCHAR(16) NOT NULL,
    category         VARCHAR(32) NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    is_secret        BOOLEAN NOT NULL DEFAULT 0,
    requires_restart BOOLEAN NOT NULL DEFAULT 0,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_system_settings_category
    ON system_settings(category);

CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    op          VARCHAR(32) NOT NULL,
    dedup_key   VARCHAR(128) NOT NULL DEFAULT '',
    payload     TEXT NOT NULL DEFAULT '{}',
    fail_count  INTEGER NOT NULL DEFAULT 0,
    enqueued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at  DATETIME
);

CREATE INDEX IF NOT EXISTS idx_task_pending_ops_scope
    ON task_pending_ops(task_type, scope, scope_id, id);
CREATE INDEX IF NOT EXISTS idx_task_pending_ops_tenant
    ON task_pending_ops(tenant_id);

CREATE TABLE IF NOT EXISTS task_dead_letters (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    related_id  VARCHAR(64) NOT NULL DEFAULT '',
    payload     TEXT NOT NULL,
    last_error  TEXT NOT NULL DEFAULT '',
    fail_count  INTEGER NOT NULL,
    failed_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_task_dead_letters_scope
    ON task_dead_letters(scope, scope_id, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_dead_letters_tenant
    ON task_dead_letters(tenant_id, failed_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_dead_letters_task_type
    ON task_dead_letters(task_type, failed_at DESC);
