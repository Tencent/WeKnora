CREATE TABLE IF NOT EXISTS session_sandbox_assignments (
    session_id        VARCHAR(36)  PRIMARY KEY,
    config_tenant_id  INTEGER      NOT NULL DEFAULT 0,
    config_id         VARCHAR(64)  NOT NULL DEFAULT '',
    machine_digest    VARCHAR(64)  NOT NULL DEFAULT '',
    released_at       DATETIME     NULL,
    cleanup_due_at    DATETIME     NULL
);
CREATE INDEX IF NOT EXISTS idx_session_sandbox_assignments_live_machine
    ON session_sandbox_assignments (machine_digest)
    WHERE released_at IS NULL AND machine_digest <> '';
CREATE INDEX IF NOT EXISTS idx_session_sandbox_assignments_cleanup_due
    ON session_sandbox_assignments (cleanup_due_at)
    WHERE cleanup_due_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS sandbox_user_machines (
    machine_digest    VARCHAR(64)  PRIMARY KEY,
    owner_tenant_id   INTEGER      NOT NULL,
    owner_id          VARCHAR(512) NOT NULL,
    config_tenant_id  INTEGER      NOT NULL,
    config_id         VARCHAR(64)  NOT NULL,
    empty_since       DATETIME     NULL
);
CREATE INDEX IF NOT EXISTS idx_sandbox_user_machines_config ON sandbox_user_machines (config_tenant_id, config_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_user_machines_owner ON sandbox_user_machines (owner_id, owner_tenant_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_user_machines_empty ON sandbox_user_machines (empty_since) WHERE empty_since IS NOT NULL;
