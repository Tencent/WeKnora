-- Migration: 000065_dingtalk_export_tasks (down)

DROP INDEX IF EXISTS idx_dingtalk_export_tasks_dentry_uuid;
DROP INDEX IF EXISTS idx_dingtalk_export_tasks_status_created;
DROP INDEX IF EXISTS idx_dingtalk_export_tasks_sync_log;
DROP INDEX IF EXISTS idx_dingtalk_export_tasks_datasource;
DROP INDEX IF EXISTS idx_dingtalk_export_tasks_task_id;

DROP TABLE IF EXISTS dingtalk_export_tasks;
