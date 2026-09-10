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
    ON task_pending_ops (task_type, scope, scope_id, id);

CREATE INDEX IF NOT EXISTS idx_task_pending_ops_tenant
    ON task_pending_ops (tenant_id);

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
    ON task_dead_letters (scope, scope_id, failed_at DESC);

CREATE INDEX IF NOT EXISTS idx_task_dead_letters_tenant
    ON task_dead_letters (tenant_id, failed_at DESC);

CREATE INDEX IF NOT EXISTS idx_task_dead_letters_task_type
    ON task_dead_letters (task_type, failed_at DESC);
