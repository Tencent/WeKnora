-- Rollback: 000072_knowledge_folder_index_pending

DROP INDEX IF EXISTS idx_knowledge_folder_index_pending_scope_updated;
DROP TABLE IF EXISTS knowledge_folder_index_pending;
