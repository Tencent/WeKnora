DO $$ BEGIN RAISE NOTICE '[Migration 000119] Creating agent_prompts and agent_run_suspensions...'; END $$;

-- One row per human-in-the-loop prompt raised by an agent run: a tool
-- approval, an in-conversation OAuth authorization, a question for the user.
-- The row, not process memory, is the source of truth for the answer so any
-- replica can record it. Kind-specific content lives in request / response.
CREATE TABLE IF NOT EXISTS agent_prompts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(64) NOT NULL DEFAULT '',
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL DEFAULT '',
    request_id VARCHAR(128) NOT NULL DEFAULT '',
    kind VARCHAR(32) NOT NULL,
    tool_call_id VARCHAR(255) NOT NULL DEFAULT '',
    suspendable BOOLEAN NOT NULL DEFAULT false,
    request JSONB,
    response JSONB,
    status VARCHAR(32) NOT NULL,
    close_reason TEXT NOT NULL DEFAULT '',
    resolved_by VARCHAR(64) NOT NULL DEFAULT '',
    resolved_at TIMESTAMPTZ,
    owner_instance VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_prompts_session_status
    ON agent_prompts (session_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_prompts_status_lease
    ON agent_prompts (status, lease_until);
CREATE INDEX IF NOT EXISTS idx_agent_prompts_status_expires
    ON agent_prompts (status, expires_at);
CREATE INDEX IF NOT EXISTS idx_agent_prompts_message
    ON agent_prompts (assistant_message_id);

-- Run state persisted when an agent run blocks on a prompt, so the run can be
-- suspended and later resumed by any replica.
CREATE TABLE IF NOT EXISTS agent_run_suspensions (
    assistant_message_id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    prompt_id VARCHAR(36) NOT NULL,
    snapshot_version INTEGER NOT NULL,
    snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agent_run_suspensions_session
    ON agent_run_suspensions (session_id);

ALTER TABLE messages ADD COLUMN IF NOT EXISTS run_state VARCHAR(32) NOT NULL DEFAULT '';

DO $$ BEGIN RAISE NOTICE '[Migration 000119] agent_prompts ready'; END $$;
