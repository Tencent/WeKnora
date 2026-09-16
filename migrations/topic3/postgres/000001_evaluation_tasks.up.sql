-- Persisted evaluation task snapshots and execution ownership.

CREATE TABLE IF NOT EXISTS evaluation_tasks (
    id                     VARCHAR(128) PRIMARY KEY,
    tenant_id              BIGINT NOT NULL,
    dataset_id             VARCHAR(255) NOT NULL,
    status                 SMALLINT NOT NULL,
    start_time             TIMESTAMPTZ NOT NULL,
    end_time               TIMESTAMPTZ,
    total                  INT NOT NULL DEFAULT 0,
    finished               INT NOT NULL DEFAULT 0,
    err_msg                TEXT NOT NULL DEFAULT '',
    cleanup_errors         JSONB NOT NULL DEFAULT '[]'::JSONB,
    params                 JSONB NOT NULL DEFAULT '{}'::JSONB,
    metric                 JSONB,
    temporary_kb_id        VARCHAR(64) NOT NULL,
    temporary_knowledge_id VARCHAR(64),
    owner_id               VARCHAR(36) NOT NULL,
    lease_expires_at       TIMESTAMPTZ,
    heartbeat_at           TIMESTAMPTZ NOT NULL,
    version                BIGINT NOT NULL DEFAULT 1,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at             TIMESTAMPTZ,
    CONSTRAINT evaluation_tasks_active_lease_check
        CHECK (status NOT IN (0, 1) OR lease_expires_at IS NOT NULL)
);

COMMENT ON TABLE evaluation_tasks IS
    'Tenant-scoped evaluation task snapshots, aggregate results, and execution ownership leases.';
COMMENT ON COLUMN evaluation_tasks.owner_id IS
    'UUID of the application instance that owns the active task or recovery attempt.';
COMMENT ON COLUMN evaluation_tasks.lease_expires_at IS
    'Active-owner lease deadline used to identify recoverable interrupted tasks.';
COMMENT ON COLUMN evaluation_tasks.version IS
    'Optimistic-lock version for task state, progress, and terminal publication.';

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_started
    ON evaluation_tasks (tenant_id, start_time DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_status
    ON evaluation_tasks (tenant_id, status, start_time DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_active_lease
    ON evaluation_tasks (status, lease_expires_at)
    WHERE deleted_at IS NULL AND status IN (0, 1);
