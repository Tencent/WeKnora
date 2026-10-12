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
    ADD COLUMN IF NOT EXISTS sandbox_config_tenant_id BIGINT NOT NULL DEFAULT 0;

COMMENT ON COLUMN sessions.sandbox_config_tenant_id IS 'Workspace owning sandbox_config_id; 0 = the session own tenant (never borrowed)';

-- Backfill from the config row itself, not from messages. The pin is sticky
-- to the first sandbox, while the newest shared-agent message may belong to a
-- later agent from a different workspace. tenant_sandbox_configs.id is the
-- primary key (a UUID), so the join names the workspace that actually owns
-- the pinned config.
--
-- Rows whose config still lives in the session's own workspace stay 0 and
-- fall back exactly as before, including sessions that mixed own agents with
-- shared ones.
UPDATE sessions s
SET sandbox_config_tenant_id = c.tenant_id
FROM tenant_sandbox_configs c
WHERE c.id = s.sandbox_config_id
  AND c.tenant_id IS DISTINCT FROM s.tenant_id
  AND COALESCE(s.sandbox_config_id, '') NOT IN ('', '-')
  AND c.deleted_at IS NULL;
