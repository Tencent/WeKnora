-- Mirrors versioned migration 000109_intent_policies: IntentGate 策略表
-- （设计文档 §6.1），版本化意图策略，按 scope 绑定、按租户隔离。

CREATE TABLE IF NOT EXISTS intent_policies (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    scope_type VARCHAR(16) NOT NULL CHECK (scope_type IN ('tool', 'service', 'agent', 'workspace', 'tenant')),
    scope_ref VARCHAR(512) NOT NULL DEFAULT '',
    arg_path VARCHAR(256),
    constraint_text TEXT NOT NULL DEFAULT '',
    rule_expr TEXT,
    risk_tier VARCHAR(8) NOT NULL DEFAULT 'low' CHECK (risk_tier IN ('low', 'high')),
    mode VARCHAR(16) NOT NULL DEFAULT 'observe' CHECK (mode IN ('observe', 'enforce')),
    version INTEGER NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_intent_policies_scope ON intent_policies (tenant_id, scope_type, scope_ref);
