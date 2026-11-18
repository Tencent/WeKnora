-- Migration: 000130_sandbox_user_allocation
-- Per-user sandbox allocation. A session's placement (its own machine, or a
-- directory on its owner's shared machine) is chosen once, on first use, and
-- recorded here; the config switch only affects sessions without a row.
DO $$ BEGIN RAISE NOTICE '[Migration 000130] Creating sandbox allocation tables'; END $$;

-- One row per placed session. machine_digest is '' for a machine of its own
-- and names the shared machine otherwise. released_at marks a deleted (or
-- retired) session; a row with it set may never be placed again, which is
-- also how a session retired before its first placement is recorded.
-- cleanup_due_at is set while the session's directory on a shared machine
-- still has to be removed.
CREATE TABLE IF NOT EXISTS session_sandbox_assignments (
    session_id        VARCHAR(36)  PRIMARY KEY,
    config_tenant_id  BIGINT       NOT NULL DEFAULT 0,
    config_id         VARCHAR(64)  NOT NULL DEFAULT '',
    machine_digest    VARCHAR(64)  NOT NULL DEFAULT '',
    released_at       TIMESTAMP WITH TIME ZONE NULL,
    cleanup_due_at    TIMESTAMP WITH TIME ZONE NULL
);
CREATE INDEX IF NOT EXISTS idx_session_sandbox_assignments_live_machine
    ON session_sandbox_assignments (machine_digest)
    WHERE released_at IS NULL AND machine_digest <> '';
CREATE INDEX IF NOT EXISTS idx_session_sandbox_assignments_cleanup_due
    ON session_sandbox_assignments (cleanup_due_at)
    WHERE cleanup_due_at IS NOT NULL;

-- Shared machines. The physical sandbox id stays in the Redis binding; this
-- table holds the identity behind each digest, answers "which machines does
-- this config / owner have", carries the empty-retention clock, and is the
-- row joins, releases and reclaims lock.
CREATE TABLE IF NOT EXISTS sandbox_user_machines (
    machine_digest    VARCHAR(64)  PRIMARY KEY,
    owner_tenant_id   BIGINT       NOT NULL,
    owner_id          VARCHAR(512) NOT NULL,
    config_tenant_id  BIGINT       NOT NULL,
    config_id         VARCHAR(64)  NOT NULL,
    empty_since       TIMESTAMP WITH TIME ZONE NULL
);
CREATE INDEX IF NOT EXISTS idx_sandbox_user_machines_config ON sandbox_user_machines (config_tenant_id, config_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_user_machines_owner ON sandbox_user_machines (owner_id, owner_tenant_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_user_machines_empty ON sandbox_user_machines (empty_since) WHERE empty_since IS NOT NULL;
