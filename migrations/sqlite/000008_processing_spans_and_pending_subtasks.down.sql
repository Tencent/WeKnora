ALTER TABLE knowledges DROP COLUMN pending_subtasks_count;

DROP INDEX IF EXISTS idx_kpspan_parent;
DROP INDEX IF EXISTS idx_kpspan_status_started;
DROP INDEX IF EXISTS idx_kpspan_knowledge_attempt;
DROP TABLE IF EXISTS knowledge_processing_spans;
