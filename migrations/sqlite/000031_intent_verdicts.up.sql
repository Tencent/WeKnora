-- Mirrors versioned migration 000111_intent_verdicts: IntentGate 判定日志
-- （设计文档 §6.2），每行一次工具调用的 Verdict 落库记录。

CREATE TABLE IF NOT EXISTS intent_verdicts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL DEFAULT '',
    tool_call_id VARCHAR(128) NOT NULL DEFAULT '',
    policy_id VARCHAR(36),
    policy_version INTEGER,
    tool_name VARCHAR(512) NOT NULL,
    args_digest VARCHAR(64) NOT NULL DEFAULT '',
    layer VARCHAR(16) NOT NULL CHECK (layer IN ('rule', 'judge', 'baseline')),
    verdict VARCHAR(32) NOT NULL CHECK (verdict IN ('allow', 'deny', 'require_approval', 'uncertain')),
    reason TEXT NOT NULL DEFAULT '',
    mode_at_decision VARCHAR(16) NOT NULL DEFAULT 'observe' CHECK (mode_at_decision IN ('observe', 'enforce')),
    latency_ms INTEGER NOT NULL DEFAULT 0,
    judge_tokens INTEGER NOT NULL DEFAULT 0,
    human_override VARCHAR(16) NOT NULL DEFAULT 'none' CHECK (human_override IN ('none', 'approved', 'modified', 'rejected')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_intent_verdicts_session ON intent_verdicts (tenant_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_intent_verdicts_policy ON intent_verdicts (tenant_id, policy_id, created_at);
