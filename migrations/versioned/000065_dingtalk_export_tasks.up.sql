-- Migration: 000065_dingtalk_export_tasks
-- Description: Persist DingTalk Markdown export jobs until callback recovery.

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating dingtalk_export_tasks...'; END $$;

CREATE TABLE IF NOT EXISTS dingtalk_export_tasks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    data_source_id VARCHAR(36) NOT NULL,
    sync_log_id VARCHAR(36),
    external_id TEXT NOT NULL,
    source_resource_id TEXT,
    workspace_id TEXT,
    node_id TEXT,
    dentry_uuid TEXT NOT NULL,
    task_id TEXT NOT NULL,
    title TEXT,
    file_name TEXT,
    source_url TEXT,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    event_id TEXT,
    export_url TEXT,
    error_code TEXT,
    error_message TEXT,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_dingtalk_export_tasks_task_id
    ON dingtalk_export_tasks(task_id);

CREATE INDEX IF NOT EXISTS idx_dingtalk_export_tasks_datasource
    ON dingtalk_export_tasks(data_source_id);

CREATE INDEX IF NOT EXISTS idx_dingtalk_export_tasks_sync_log
    ON dingtalk_export_tasks(sync_log_id);

CREATE INDEX IF NOT EXISTS idx_dingtalk_export_tasks_status_created
    ON dingtalk_export_tasks(status, created_at);

CREATE INDEX IF NOT EXISTS idx_dingtalk_export_tasks_dentry_uuid
    ON dingtalk_export_tasks(dentry_uuid);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] dingtalk_export_tasks ready'; END $$;
