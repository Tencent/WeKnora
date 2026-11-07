CREATE TABLE IF NOT EXISTS agent_collaborations (
 id VARCHAR(36) PRIMARY KEY,
 tenant_id BIGINT NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 session_id VARCHAR(36) NOT NULL,
 status VARCHAR(32) NOT NULL,
 version BIGINT NOT NULL,
 data TEXT NOT NULL,
 updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_collaborations_status ON agent_collaborations(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_agent_collaborations_owner ON agent_collaborations(tenant_id, user_id, session_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_collaborations_active_parent ON agent_collaborations(tenant_id, user_id, session_id) WHERE status IN ('active', 'cancelling');
