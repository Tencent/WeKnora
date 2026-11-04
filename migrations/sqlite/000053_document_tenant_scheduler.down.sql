DROP INDEX IF EXISTS idx_document_pending_tenant;
ALTER TABLE task_pending_ops DROP COLUMN available_at;
DROP TABLE IF EXISTS document_execution_leases;
DROP TABLE IF EXISTS document_scheduler_state;
DROP INDEX IF EXISTS idx_document_terminal_attempt;
ALTER TABLE task_pending_ops DROP COLUMN generation;
ALTER TABLE task_dead_letters DROP COLUMN generation;
DROP TABLE IF EXISTS document_generations;
