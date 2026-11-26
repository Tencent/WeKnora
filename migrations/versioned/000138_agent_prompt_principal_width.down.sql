-- Migration: 000138_agent_prompt_principal_width (down)
-- Narrowing back is lossy: principal identities longer than 64 characters are
-- truncated by the USING clauses so the ALTER cannot fail on existing rows.
DO $$ BEGIN RAISE NOTICE '[Migration 000138 down] Narrowing agent_prompts principal identities to VARCHAR(64)'; END $$;

ALTER TABLE agent_prompts
    ALTER COLUMN user_id TYPE VARCHAR(64) USING LEFT(user_id, 64),
    ALTER COLUMN resolved_by TYPE VARCHAR(64) USING LEFT(resolved_by, 64);
