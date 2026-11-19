-- Migration: 000137_agent_prompt_embed_principal_width (down)
-- Restoring VARCHAR(64) cannot hold a 93-char embed principal, so the rollback
-- clips the tail rather than failing the step (same idiom as 000135 down).
-- Prompt rows written by the embed plane lose their session suffix on the way back.
ALTER TABLE agent_prompts
    ALTER COLUMN user_id TYPE VARCHAR(64) USING LEFT(user_id, 64);
ALTER TABLE agent_prompts
    ALTER COLUMN resolved_by TYPE VARCHAR(64) USING LEFT(resolved_by, 64);
