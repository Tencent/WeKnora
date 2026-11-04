-- Mirrors versioned migration 000137_message_run_budget.
ALTER TABLE messages ADD COLUMN run_budget TEXT;
