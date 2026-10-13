-- Migration 000117: agent inbox — durable event stream for agent situational
-- awareness (agent-sidebar-inbox, P1 MVP).
--
-- One row = one event worth surfacing to the sidebar agent (user actions,
-- KB backend lifecycle events, agent action receipts). The inbox is NEVER
-- part of the messages table on purpose: agent history assembly, memory
-- extraction and session fork all iterate message rows, and events must not
-- leak into any of them. The agent reads the inbox on demand via the
-- check_inbox tool; retention is policy-driven (see
-- config/agent_inbox_policy.yaml and service.AgentInboxService).
CREATE TABLE IF NOT EXISTS agent_inbox (
    id          VARCHAR(36) PRIMARY KEY,
    tenant_id   BIGINT      NOT NULL,
    kb_id       VARCHAR(36) NOT NULL DEFAULT '',
    user_id     VARCHAR(64) NOT NULL DEFAULT '',
    type        VARCHAR(64) NOT NULL,
    priority    VARCHAR(8)  NOT NULL DEFAULT 'P2',
    payload     JSONB       NOT NULL DEFAULT '{}',
    created_at  TIMESTAMP   NOT NULL DEFAULT CURRENT_TIMESTAMP,
    read_at     TIMESTAMP   NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_inbox_scope
    ON agent_inbox (tenant_id, kb_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_inbox_owner
    ON agent_inbox (tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_inbox_retention
    ON agent_inbox (priority, created_at);
