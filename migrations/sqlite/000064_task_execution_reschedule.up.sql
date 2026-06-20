-- Migration: 000064_task_execution_reschedule
-- Description: Preserve root execution handoff chains when lease contention
--              reschedules a concrete asynq execution.

ALTER TABLE task_executions
    ADD COLUMN rescheduled_to_execution_id VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_task_executions_rescheduled_to
    ON task_executions(rescheduled_to_execution_id);

