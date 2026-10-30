DO $$ BEGIN RAISE NOTICE '[Migration 000135] Adding messages.run_budget...'; END $$;

-- Effective RunContract and cumulative usage for the assistant turn.
ALTER TABLE messages ADD COLUMN IF NOT EXISTS run_budget JSONB;
