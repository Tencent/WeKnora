-- Migration: 000137_agent_prompt_embed_principal_width
-- Description: Widen agent_prompts.user_id and agent_prompts.resolved_by from VARCHAR(64) to VARCHAR(512).

DO $$ BEGIN RAISE NOTICE '[Migration 000137] Widening agent_prompts identity columns to VARCHAR(512)...'; END $$;

-- An embed visitor has no user account. The principal of the run is
-- types.EmbedSessionPrincipal(tenant, channel, session), whose StorageID() is
-- "embed_session:<tenant>:<channel-uuid>:<session-uuid>" -- 93 characters for a
-- typical id (ensureEmbedSession in internal/handler/embed_channel.go mints it;
-- sessions.user_id already stores that same string and is VARCHAR(512)). The HITL
-- prompt rows store it too: a tool-approval / ask_user prompt takes meta.UserID =
-- principal.StorageID() (the tool-exec context in internal/agent/act.go), and the
-- answer stores req.UserID, which the resolve handler reads back off the same
-- principal (internal/handler/mcp_service.go, written by
-- internal/agent/hitl/broker.go).
--
-- Both columns were VARCHAR(64), so PostgreSQL rejected the prompt insert with
-- 22001 value too long before any card could be emitted. The embed page never
-- showed a tool-approval or ask_user card at all: the tool call failed and the
-- agent quoted the raw SQL error to the visitor. Widening to 512 matches the
-- column that already holds this id losslessly; clipping the id instead would
-- collide two sessions of the same channel, which share a 64-char prefix.
--
-- No SQLite counterpart is needed: SQLite does not enforce varchar lengths and
-- has no ALTER COLUMN, so a delta migration could not retype it (000135).
ALTER TABLE agent_prompts ALTER COLUMN user_id TYPE VARCHAR(512);
ALTER TABLE agent_prompts ALTER COLUMN resolved_by TYPE VARCHAR(512);
