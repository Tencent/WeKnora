-- Mirrors versioned migration 000110_intent_verdicts_judge_model（T61，
-- issue #24）：verdict 行记录判定时用的 judge 模型 ID，语料导出按此分层。

ALTER TABLE intent_verdicts ADD COLUMN judge_model VARCHAR(64) NOT NULL DEFAULT '';
