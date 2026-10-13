-- Agent inbox (see versioned/000118 for the design note).
CREATE TABLE IF NOT EXISTS agent_inbox (
    id          TEXT PRIMARY KEY,
    tenant_id   INTEGER NOT NULL,
    kb_id       TEXT    NOT NULL DEFAULT '',
    user_id     TEXT    NOT NULL DEFAULT '',
    type        TEXT    NOT NULL,
    priority    TEXT    NOT NULL DEFAULT 'P2',
    payload     TEXT    NOT NULL DEFAULT '{}',
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    read_at     TIMESTAMP NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_inbox_scope
    ON agent_inbox (tenant_id, kb_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_inbox_owner
    ON agent_inbox (tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_inbox_retention
    ON agent_inbox (priority, created_at);
