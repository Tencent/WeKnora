-- Mirrors versioned migration 000135_message_run_budget.
ALTER TABLE messages ADD COLUMN run_budget TEXT;
