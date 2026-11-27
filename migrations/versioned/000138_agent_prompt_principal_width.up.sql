-- Migration: 000138_agent_prompt_principal_width (up)
-- Principal.StorageID values include embed session identifiers and may exceed
-- 64 characters. Keep prompt ownership and resolution identities lossless.
DO $$ BEGIN RAISE NOTICE '[Migration 000138] Widening agent_prompts principal identities to VARCHAR(512)'; END $$;

ALTER TABLE agent_prompts
    ALTER COLUMN user_id TYPE VARCHAR(512),
    ALTER COLUMN resolved_by TYPE VARCHAR(512);
