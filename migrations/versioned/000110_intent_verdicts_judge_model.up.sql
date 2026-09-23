-- Migration: 000110_intent_verdicts_judge_model
-- T61（issue #24）语料导出按 judge 模型分层：verdict 行记录判定时用的
-- judge 模型 ID（设计 §12 决策 2「judge 模型归属」——杂牌 judge 产生的
-- verdict 语料质量方差大，蒸馏前按 judge 模型过滤）。规则层/baseline
-- 判定无 judge 参与，落默认空串。
DO $$ BEGIN RAISE NOTICE '[Migration 000110] Adding intent_verdicts.judge_model'; END $$;

ALTER TABLE intent_verdicts ADD COLUMN IF NOT EXISTS judge_model VARCHAR(64) NOT NULL DEFAULT '';

COMMENT ON COLUMN intent_verdicts.judge_model IS '判定时使用的 judge 模型 ID（models.id，varchar(64)）；规则层/baseline 判定为空串。T61 语料导出按此分层过滤（设计 §12 决策 2）';
