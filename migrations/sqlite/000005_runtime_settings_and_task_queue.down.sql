DROP INDEX IF EXISTS idx_task_dead_letters_task_type;
DROP INDEX IF EXISTS idx_task_dead_letters_tenant;
DROP INDEX IF EXISTS idx_task_dead_letters_scope;
DROP TABLE IF EXISTS task_dead_letters;

DROP INDEX IF EXISTS idx_task_pending_ops_tenant;
DROP INDEX IF EXISTS idx_task_pending_ops_scope;
DROP TABLE IF EXISTS task_pending_ops;

DROP INDEX IF EXISTS idx_system_settings_category;
DROP TABLE IF EXISTS system_settings;

DROP INDEX IF EXISTS idx_users_is_system_admin;
ALTER TABLE users DROP COLUMN is_system_admin;

ALTER TABLE knowledges DROP COLUMN pending_subtasks_count;
