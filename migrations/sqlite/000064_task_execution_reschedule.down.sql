-- SQLite rollback intentionally leaves rescheduled_to_execution_id in place on
-- older engines where DROP COLUMN is unavailable. The column is additive.

