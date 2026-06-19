-- Migration: 000062_task_center_ledger
-- Description: Durable task center ledger tables for user-visible jobs and
--              concrete execution attempts.

DO $$ BEGIN RAISE NOTICE '[Migration 000062] Applying task center ledger schema'; END $$;

CREATE TABLE IF NOT EXISTS task_jobs (
    job_id           VARCHAR(64) PRIMARY KEY,
    tenant_id        BIGINT NOT NULL,
    created_by       VARCHAR(64) NOT NULL DEFAULT '',
    kind             VARCHAR(32) NOT NULL,
    origin           VARCHAR(8) NOT NULL DEFAULT 'user',
    display_name     VARCHAR(255) NOT NULL DEFAULT '',
    scope            VARCHAR(32) NOT NULL,
    scope_id         VARCHAR(64) NOT NULL,
    related_id       VARCHAR(64) NOT NULL DEFAULT '',
    process_attempt  INT NOT NULL DEFAULT 0,
    state            VARCHAR(16) NOT NULL DEFAULT 'queued',
    metadata         JSONB NOT NULL DEFAULT '{}'::JSONB,
    replay_spec      JSONB NOT NULL DEFAULT '{}'::JSONB,
    last_error_class VARCHAR(24) NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    failed_task_type VARCHAR(64) NOT NULL DEFAULT '',
    failed_task_id   VARCHAR(64) NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at      TIMESTAMPTZ
);

COMMENT ON TABLE task_jobs IS 'Durable user-visible logical task ledger. Lists and counts read this table, not Redis/asynq Inspector.';
COMMENT ON COLUMN task_jobs.process_attempt IS 'Business processing round. Asynq automatic retry does not change it; user retry increments it.';
COMMENT ON COLUMN task_jobs.replay_spec IS 'Versioned minimal replay reference. Not exposed by normal task center APIs.';

CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_state_created
    ON task_jobs (tenant_id, state, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_kind_state
    ON task_jobs (tenant_id, kind, state);
CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_creator_created
    ON task_jobs (tenant_id, created_by, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_jobs_scope_attempt
    ON task_jobs (tenant_id, scope, scope_id, process_attempt);
CREATE INDEX IF NOT EXISTS idx_task_jobs_related_state
    ON task_jobs (tenant_id, related_id, state);

CREATE TABLE IF NOT EXISTS task_executions (
    execution_id    VARCHAR(64) PRIMARY KEY,
    job_id          VARCHAR(64) NOT NULL REFERENCES task_jobs(job_id) ON DELETE CASCADE,
    process_attempt INT NOT NULL DEFAULT 0,
    task_type       VARCHAR(64) NOT NULL,
    queue           VARCHAR(32) NOT NULL DEFAULT '',
    state           VARCHAR(16) NOT NULL DEFAULT 'queued',
    retry_count     INT NOT NULL DEFAULT 0,
    error_class     VARCHAR(24) NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    retry_of        VARCHAR(64) NOT NULL DEFAULT '',
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    dispatched_at   TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ
);

COMMENT ON TABLE task_executions IS 'Concrete root execution history for task_jobs. execution_id doubles as the asynq task ID.';
COMMENT ON COLUMN task_executions.retry_count IS 'Asynq/lite automatic retry count; distinct from task_jobs.process_attempt.';

CREATE INDEX IF NOT EXISTS idx_task_executions_job_attempt_enqueued
    ON task_executions (job_id, process_attempt, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_task_executions_job_state
    ON task_executions (job_id, state);
CREATE INDEX IF NOT EXISTS idx_task_executions_state_enqueued
    ON task_executions (state, enqueued_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000062] task center ledger schema applied successfully'; END $$;
