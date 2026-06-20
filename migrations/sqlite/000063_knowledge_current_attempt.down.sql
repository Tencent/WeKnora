DROP INDEX IF EXISTS idx_knowledges_current_attempt;
DROP INDEX IF EXISTS uq_task_jobs_scope_attempt;
ALTER TABLE knowledges DROP COLUMN current_process_attempt;
