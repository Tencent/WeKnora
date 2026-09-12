-- Persistent cancel requests for evaluation tasks (mirrors sqlite 000014).

ALTER TABLE evaluation_tasks
    ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ;

COMMENT ON COLUMN evaluation_tasks.cancel_requested_at IS
    'First persistent user-cancel request time; terminal CAS truth separates Canceled from other terminal states.';

ALTER TABLE evaluation_tasks
    ADD CONSTRAINT evaluation_tasks_canceled_requires_request
        CHECK (status <> 6 OR cancel_requested_at IS NOT NULL),
    ADD CONSTRAINT evaluation_tasks_terminal_without_cancel
        CHECK (status NOT IN (2, 3, 4, 5) OR cancel_requested_at IS NULL);
