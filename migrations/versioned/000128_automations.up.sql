-- Scheduled agent tasks, their runs, and in-app notifications.
CREATE TABLE IF NOT EXISTS automations (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    owner_user_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    agent_id VARCHAR(64) NOT NULL,
    instruction TEXT NOT NULL,
    trigger_config TEXT NOT NULL,
    session_mode VARCHAR(16) NOT NULL DEFAULT 'per_run',
    persistent_session_id VARCHAR(36) NOT NULL DEFAULT '',
    carry_last_result BOOLEAN NOT NULL DEFAULT FALSE,
    misfire_policy VARCHAR(16) NOT NULL DEFAULT 'skip',
    overlap_policy VARCHAR(16) NOT NULL DEFAULT 'skip',
    access_level VARCHAR(16) NOT NULL DEFAULT 'confirm',
    approval_timeout_minutes INTEGER NOT NULL DEFAULT 1440,
    limits TEXT NOT NULL DEFAULT '{}',
    notify_on_success BOOLEAN NOT NULL DEFAULT FALSE,
    next_run_at TIMESTAMPTZ,
    last_run_at TIMESTAMPTZ,
    last_success_at TIMESTAMPTZ,
    last_run_status VARCHAR(32) NOT NULL DEFAULT '',
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    paused_reason TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_automations_due ON automations(enabled, next_run_at);
CREATE INDEX IF NOT EXISTS idx_automations_owner ON automations(tenant_id, owner_user_id);
CREATE INDEX IF NOT EXISTS idx_automations_deleted_at ON automations(deleted_at);

CREATE TABLE IF NOT EXISTS automation_runs (
    id VARCHAR(36) PRIMARY KEY,
    automation_id VARCHAR(36) NOT NULL,
    tenant_id BIGINT NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL,
    trigger_kind VARCHAR(16) NOT NULL,
    status VARCHAR(32) NOT NULL,
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    user_message_id VARCHAR(36) NOT NULL DEFAULT '',
    assistant_message_id VARCHAR(36) NOT NULL DEFAULT '',
    owner VARCHAR(36) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    pending_prompt_id VARCHAR(36) NOT NULL DEFAULT '',
    resume BOOLEAN NOT NULL DEFAULT FALSE,
    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,
    answer TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    usage TEXT,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_automation_runs_slot
    ON automation_runs(automation_id, scheduled_at, trigger_kind);
CREATE INDEX IF NOT EXISTS idx_automation_runs_automation ON automation_runs(automation_id);
CREATE INDEX IF NOT EXISTS idx_automation_runs_status ON automation_runs(status);

CREATE TABLE IF NOT EXISTS notifications (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    title VARCHAR(255) NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    link TEXT NOT NULL DEFAULT '',
    source_type VARCHAR(32) NOT NULL DEFAULT '',
    source_id VARCHAR(36) NOT NULL DEFAULT '',
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(tenant_id, user_id, created_at);
