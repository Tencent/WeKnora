-- Mirrors versioned migration 000119_agent_prompts:
-- persisted human-in-the-loop prompts and suspended agent run state.

CREATE TABLE IF NOT EXISTS agent_prompts (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL,
    assistant_message_id TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    tool_call_id TEXT NOT NULL DEFAULT '',
    suspendable BOOLEAN NOT NULL DEFAULT 0,
    request TEXT,
    response TEXT,
    status TEXT NOT NULL,
    close_reason TEXT NOT NULL DEFAULT '',
    resolved_by TEXT NOT NULL DEFAULT '',
    resolved_at DATETIME,
    owner_instance TEXT NOT NULL DEFAULT '',
    lease_until DATETIME,
    expires_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_prompts_session_status
    ON agent_prompts (session_id, status);
CREATE INDEX IF NOT EXISTS idx_agent_prompts_status_lease
    ON agent_prompts (status, lease_until);
CREATE INDEX IF NOT EXISTS idx_agent_prompts_status_expires
    ON agent_prompts (status, expires_at);
CREATE INDEX IF NOT EXISTS idx_agent_prompts_message
    ON agent_prompts (assistant_message_id);

CREATE TABLE IF NOT EXISTS agent_run_suspensions (
    assistant_message_id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id TEXT NOT NULL,
    prompt_id TEXT NOT NULL,
    snapshot_version INTEGER NOT NULL,
    snapshot TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_agent_run_suspensions_session
    ON agent_run_suspensions (session_id);

ALTER TABLE messages ADD COLUMN run_state TEXT NOT NULL DEFAULT '';
