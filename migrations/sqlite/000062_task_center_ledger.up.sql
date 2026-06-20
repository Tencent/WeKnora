-- Migration: 000062_task_center_ledger
-- Description: Durable task center ledger tables for user-visible jobs and
--              concrete execution attempts.

-- SQLite stores JSON-shaped fields as TEXT; application code still treats
-- them as JSON.
CREATE TABLE IF NOT EXISTS task_jobs (
    job_id VARCHAR(64) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    kind VARCHAR(32) NOT NULL,
    origin VARCHAR(8) NOT NULL DEFAULT 'user',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    process_attempt INTEGER NOT NULL DEFAULT 0,
    state VARCHAR(16) NOT NULL DEFAULT 'queued',
    metadata TEXT NOT NULL DEFAULT '{}',
    replay_spec TEXT NOT NULL DEFAULT '{}',
    last_error_class VARCHAR(24) NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    failed_task_type VARCHAR(64) NOT NULL DEFAULT '',
    failed_task_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_state_created
    ON task_jobs(tenant_id, state, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_kind_state
    ON task_jobs(tenant_id, kind, state);
CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_creator_created
    ON task_jobs(tenant_id, created_by, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_jobs_scope_attempt
    ON task_jobs(tenant_id, scope, scope_id, process_attempt);
CREATE INDEX IF NOT EXISTS idx_task_jobs_related_state
    ON task_jobs(tenant_id, related_id, state);

CREATE TABLE IF NOT EXISTS task_executions (
    execution_id VARCHAR(64) PRIMARY KEY,
    job_id VARCHAR(64) NOT NULL REFERENCES task_jobs(job_id) ON DELETE CASCADE,
    process_attempt INTEGER NOT NULL DEFAULT 0,
    task_type VARCHAR(64) NOT NULL,
    queue VARCHAR(32) NOT NULL DEFAULT '',
    state VARCHAR(16) NOT NULL DEFAULT 'queued',
    retry_count INTEGER NOT NULL DEFAULT 0,
    error_class VARCHAR(24) NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    retry_of VARCHAR(64) NOT NULL DEFAULT '',
    enqueued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    dispatched_at DATETIME,
    started_at DATETIME,
    finished_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_task_executions_job_attempt_enqueued
    ON task_executions(job_id, process_attempt, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_task_executions_job_state
    ON task_executions(job_id, state);
CREATE INDEX IF NOT EXISTS idx_task_executions_state_enqueued
    ON task_executions(state, enqueued_at);
