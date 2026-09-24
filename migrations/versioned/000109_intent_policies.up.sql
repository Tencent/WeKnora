-- Migration: 000109_intent_policies
-- IntentGate 策略表（设计文档 docs/plans/2026-09-21-intent-gate-design.md
-- §6.1）：IntentPolicy 是版本化的配置资产，绑定 scope
-- （tool/service/agent/workspace/tenant），含 NLC 原文、可编译规则表达式
-- 与 mode；按租户隔离。术语见 CONTEXT.md（IntentPolicy / NLC / Observe /
-- Enforce）。
DO $$ BEGIN RAISE NOTICE '[Migration 000109] Creating intent_policies table'; END $$;

CREATE TABLE IF NOT EXISTS intent_policies (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
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
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_intent_policies_scope ON intent_policies (tenant_id, scope_type, scope_ref);

COMMENT ON TABLE intent_policies IS 'IntentGate 策略表：版本化意图策略，按 scope 绑定、按租户隔离（设计 §6.1）';
COMMENT ON COLUMN intent_policies.scope_ref IS 'tool 级为 service_id:tool_name，支持 *:wiki_* 前缀通配；tenant 级可为空';
COMMENT ON COLUMN intent_policies.arg_path IS '参数路径表达式（如 $.amount）；NULL = 整条调用';
COMMENT ON COLUMN intent_policies.rule_expr IS '确定性表达式（value <= 75）；NULL = 走语义层 judge';
COMMENT ON COLUMN intent_policies.version IS '每次修改 +1，旧版本保留（设计 §3.2）；同级命中取 version 最新（§8.3）';
COMMENT ON COLUMN intent_policies.mode IS 'observe 只记录不拦截，新策略一律 observe 起步（§9）';
