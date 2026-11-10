DROP INDEX IF EXISTS idx_task_dead_letters_task_type;
DROP INDEX IF EXISTS idx_task_dead_letters_tenant;
DROP INDEX IF EXISTS idx_task_dead_letters_scope;
DROP INDEX IF EXISTS idx_task_pending_ops_tenant;
DROP INDEX IF EXISTS idx_task_pending_ops_scope;

DROP TABLE IF EXISTS task_dead_letters;
DROP TABLE IF EXISTS task_pending_ops;
