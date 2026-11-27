-- Migration: 000138_agent_prompt_principal_width
-- Widens the identity columns on agent_prompts so embed-session principals fit.
--
-- Embed sessions answer prompts with the session-owner principal
-- "embed_session:<tenant>:<channel>:<session>" (93 characters for current
-- UUIDs). Both columns were VARCHAR(64), so creating an in-conversation MCP
-- OAuth prompt, ask_user prompt or tool approval for an embed visitor failed
-- with SQLSTATE 22001 and no prompt row was stored. The agent then saw
-- "needs_auth" and the visitor never received an authorization card.
--
-- resolved_by is widened as well because the same principal records the
-- answer when the visitor authorizes the prompt.
DO $$ BEGIN RAISE NOTICE '[Migration 000138] Widening agent_prompts identity columns to VARCHAR(255)'; END $$;

ALTER TABLE agent_prompts ALTER COLUMN user_id TYPE VARCHAR(255);
ALTER TABLE agent_prompts ALTER COLUMN resolved_by TYPE VARCHAR(255);