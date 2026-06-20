-- NOTE: 000063.up moves duplicate task_jobs process_attempt values into a
-- negative legacy namespace before creating uq_task_jobs_scope_attempt. This
-- down migration removes the new schema objects, but intentionally does not
-- restore those duplicate positive attempts because doing so would reintroduce
-- ambiguous task ownership history.
DROP INDEX IF EXISTS idx_knowledges_current_attempt;
DROP INDEX IF EXISTS uq_task_jobs_scope_attempt;
ALTER TABLE knowledges DROP COLUMN IF EXISTS current_process_attempt;
