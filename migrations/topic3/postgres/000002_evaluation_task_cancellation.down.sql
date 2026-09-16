ALTER TABLE evaluation_tasks
    DROP CONSTRAINT IF EXISTS evaluation_tasks_canceled_requires_request,
    DROP CONSTRAINT IF EXISTS evaluation_tasks_terminal_without_cancel;

ALTER TABLE evaluation_tasks
    DROP COLUMN IF EXISTS cancel_requested_at;
