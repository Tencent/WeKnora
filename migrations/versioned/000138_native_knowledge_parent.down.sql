DROP TRIGGER IF EXISTS knowledge_native_deleting ON knowledges;
DROP FUNCTION IF EXISTS preserve_knowledge_deleting_state();
DROP TRIGGER IF EXISTS knowledge_native_parent_delete ON knowledges;
DROP TRIGGER IF EXISTS knowledge_native_parent ON knowledges;
DROP FUNCTION IF EXISTS detach_deleted_knowledge_children();
DROP FUNCTION IF EXISTS validate_native_knowledge_parent();
DROP INDEX IF EXISTS idx_knowledge_parent;
ALTER TABLE knowledges DROP COLUMN parent_knowledge_id;
ALTER TABLE knowledges DROP COLUMN deletion_pending;
