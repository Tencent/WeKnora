-- Reverse 000018: drop experiment snapshot columns and index.
-- SQLite cannot DROP COLUMN before 3.35; rebuild the table without the four columns.

DROP INDEX IF EXISTS idx_evaluation_tasks_dataset_version;

CREATE TABLE IF NOT EXISTS evaluation_tasks_backup_000018 (
    id                     TEXT PRIMARY KEY,
    tenant_id              INTEGER NOT NULL,
    dataset_id             TEXT NOT NULL,
    status                 INTEGER NOT NULL,
    start_time             DATETIME NOT NULL,
    end_time               DATETIME,
    total                  INTEGER NOT NULL DEFAULT 0,
    finished               INTEGER NOT NULL DEFAULT 0,
    err_msg                TEXT NOT NULL DEFAULT '',
    cleanup_errors         TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(cleanup_errors)),
    params                 TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(params)),
    metric                 TEXT CHECK (metric IS NULL OR json_valid(metric)),
    temporary_kb_id        TEXT NOT NULL,
    temporary_knowledge_id TEXT,
    owner_id               TEXT NOT NULL,
    lease_expires_at       DATETIME,
    heartbeat_at           DATETIME NOT NULL,
    version                INTEGER NOT NULL DEFAULT 1,
    created_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at             DATETIME,
    cancel_requested_at    DATETIME,
    CONSTRAINT evaluation_tasks_active_lease_check
        CHECK (status NOT IN (0, 1) OR lease_expires_at IS NOT NULL),
    CONSTRAINT evaluation_tasks_canceled_requires_request
        CHECK (status <> 6 OR cancel_requested_at IS NOT NULL),
    CONSTRAINT evaluation_tasks_terminal_without_cancel
        CHECK (status NOT IN (2, 3, 4, 5) OR cancel_requested_at IS NULL)
);

INSERT INTO evaluation_tasks_backup_000018 (
    id, tenant_id, dataset_id, status, start_time, end_time, total, finished, err_msg,
    cleanup_errors, params, metric, temporary_kb_id, temporary_knowledge_id, owner_id,
    lease_expires_at, heartbeat_at, version, created_at, updated_at, deleted_at,
    cancel_requested_at
)
SELECT
    id, tenant_id, dataset_id, status, start_time, end_time, total, finished, err_msg,
    cleanup_errors, params, metric, temporary_kb_id, temporary_knowledge_id, owner_id,
    lease_expires_at, heartbeat_at, version, created_at, updated_at, deleted_at,
    cancel_requested_at
FROM evaluation_tasks;

DROP TABLE evaluation_tasks;
ALTER TABLE evaluation_tasks_backup_000018 RENAME TO evaluation_tasks;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_started
    ON evaluation_tasks (tenant_id, start_time DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_status_started
    ON evaluation_tasks (tenant_id, status, start_time DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_active_lease
    ON evaluation_tasks (status, lease_expires_at)
    WHERE deleted_at IS NULL AND status IN (0, 1);

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_retention
    ON evaluation_tasks (end_time)
    WHERE status IN (2, 3, 4, 5, 6) AND end_time IS NOT NULL;
