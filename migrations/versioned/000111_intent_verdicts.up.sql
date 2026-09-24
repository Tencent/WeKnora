-- Migration: 000111_intent_verdicts
-- IntentGate 判定日志（设计文档 docs/plans/2026-09-21-intent-gate-design.md
-- §6.2）：每行是一次工具调用的 Verdict 落库记录，是策略运营报表与数据飞轮
-- 的数据源。术语见 CONTEXT.md（IntentGate / Verdict / Observe / Enforce）。
DO $$ BEGIN RAISE NOTICE '[Migration 000111] Creating intent_verdicts table'; END $$;

CREATE TABLE IF NOT EXISTS intent_verdicts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
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
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_intent_verdicts_session ON intent_verdicts (tenant_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_intent_verdicts_policy ON intent_verdicts (tenant_id, policy_id, created_at);

COMMENT ON TABLE intent_verdicts IS 'IntentGate 判定日志：每次工具调用的 Verdict，可回链 Langfuse trace（设计 §6.2）';
COMMENT ON COLUMN intent_verdicts.policy_id IS 'NULL = 兜底判定（无策略命中时的基线扫描，layer=baseline）';
COMMENT ON COLUMN intent_verdicts.args_digest IS '工具参数的 sha256 hex（规范化 JSON 后计算）；敏感参数（SQL 类）原文永不落库';
COMMENT ON COLUMN intent_verdicts.mode_at_decision IS '判定时的策略 mode：observe 期的 deny 也照记';
COMMENT ON COLUMN intent_verdicts.human_override IS '人工后续动作（审批改参数等），数据飞轮关键字段';
