-- Migration: 000138_agent_prompt_principal_width (down)
-- Narrowing back is lossy: embed-session principals longer than 64 characters
-- are truncated by the USING clause so the ALTER cannot fail on existing rows.
DO $$ BEGIN RAISE NOTICE '[Migration 000138 down] Narrowing agent_prompts identity columns to VARCHAR(64)'; END $$;

ALTER TABLE agent_prompts
    ALTER COLUMN user_id TYPE VARCHAR(64) USING LEFT(user_id, 64);
ALTER TABLE agent_prompts
    ALTER COLUMN resolved_by TYPE VARCHAR(64) USING LEFT(resolved_by, 64);