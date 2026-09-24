-- Mirrors versioned migration 000110_intent_verdicts_judge_model (rollback)。

-- sqlite 不支持 DROP COLUMN 的版本较旧；3.35+ 支持。WeKnora 内嵌
-- sqlite 版本满足，仍保留标准写法。
ALTER TABLE intent_verdicts DROP COLUMN judge_model;
