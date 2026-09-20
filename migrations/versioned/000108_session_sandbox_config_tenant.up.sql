-- Migration: 000108_session_sandbox_config_tenant
-- sessions.sandbox_config_id alone is not an address for a sandbox backend.
-- tenant_sandbox_configs is keyed by (tenant_id, id), so the same config id
-- resolves in exactly one workspace — and a shared agent runs on ITS OWNER's
-- config while the session belongs to the borrower.
--
-- Everything inside a chat turn happened to work because the turn already runs
-- under the agent owner (see WithExecutionTenant in the QA handler). Everything
-- outside it — session teardown, the terminal and desktop panels, fork
-- snapshots — reads the ambient request tenant, which is the borrower, and
-- finds nothing. Teardown then logs a warning and returns, leaving a paused
-- MicroVM nobody holds the id of (Cube/E2B create with onTimeout=pause).
--
-- Recording the owning workspace next to the pin makes the pair travel
-- together. 0 means "the session's own tenant", which is every sandbox created
-- by an agent the session's workspace owns.
DO $$ BEGIN RAISE NOTICE '[Migration 000108] Adding sessions.sandbox_config_tenant_id'; END $$;

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS sandbox_config_tenant_id INTEGER NOT NULL DEFAULT 0;

COMMENT ON COLUMN sessions.sandbox_config_tenant_id IS 'Workspace owning sandbox_config_id; 0 = the session own tenant (never borrowed)';

-- Backfill the sessions that are broken today: a pinned config that does NOT
-- exist in the session's own workspace can only have come from a shared agent,
-- and that turn recorded the lending workspace on its assistant message.
--
-- The NOT EXISTS guard is what keeps this safe for sessions that mixed their
-- own agents with shared ones: whenever the session's workspace really owns the
-- pinned config, the row stays 0 and falls back exactly as before.
UPDATE sessions s
SET sandbox_config_tenant_id = m.agent_tenant_id
FROM (
    SELECT DISTINCT ON (session_id) session_id, agent_tenant_id
    FROM messages
    WHERE agent_tenant_id <> 0
    ORDER BY session_id, created_at DESC
) m
WHERE s.id = m.session_id
  AND COALESCE(s.sandbox_config_id, '') NOT IN ('', '-')
  AND NOT EXISTS (
      SELECT 1 FROM tenant_sandbox_configs c
      WHERE c.tenant_id = s.tenant_id AND c.id = s.sandbox_config_id
  );
