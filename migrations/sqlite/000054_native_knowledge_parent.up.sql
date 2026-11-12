ALTER TABLE knowledges ADD COLUMN parent_knowledge_id VARCHAR(36) REFERENCES knowledges(id) ON DELETE SET NULL;
ALTER TABLE knowledges ADD COLUMN deletion_pending BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX idx_knowledge_parent ON knowledges(parent_knowledge_id) WHERE deleted_at IS NULL;
CREATE TRIGGER knowledge_native_parent_insert BEFORE INSERT ON knowledges WHEN NEW.parent_knowledge_id IS NOT NULL
BEGIN

 SELECT CASE WHEN NEW.parent_knowledge_id=NEW.id THEN RAISE(ABORT,'knowledge parent cannot be itself') END;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM knowledges WHERE id=NEW.parent_knowledge_id AND tenant_id=NEW.tenant_id
  AND knowledge_base_id=NEW.knowledge_base_id AND deleted_at IS NULL AND parse_status != 'deleting' AND NOT deletion_pending) THEN RAISE(ABORT,'parent knowledge scope/state invalid') END;
 SELECT CASE WHEN EXISTS(WITH RECURSIVE lineage(id,parent,depth) AS (
  SELECT id,parent_knowledge_id,1 FROM knowledges WHERE id=NEW.parent_knowledge_id
  UNION ALL SELECT k.id,k.parent_knowledge_id,l.depth+1 FROM knowledges k JOIN lineage l ON k.id=l.parent WHERE l.depth<=32
 ) SELECT 1 FROM lineage WHERE id=NEW.id OR depth>32) THEN RAISE(ABORT,'knowledge parent cycle/depth limit') END;
END;
CREATE TRIGGER knowledge_native_parent_update BEFORE UPDATE OF parent_knowledge_id ON knowledges
 WHEN NEW.parent_knowledge_id IS NOT NULL AND NEW.parent_knowledge_id IS NOT OLD.parent_knowledge_id
BEGIN

 SELECT CASE WHEN NEW.parent_knowledge_id=NEW.id THEN RAISE(ABORT,'knowledge parent cannot be itself') END;
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM knowledges WHERE id=NEW.parent_knowledge_id AND tenant_id=NEW.tenant_id
  AND knowledge_base_id=NEW.knowledge_base_id AND deleted_at IS NULL AND parse_status != 'deleting' AND NOT deletion_pending) THEN RAISE(ABORT,'parent knowledge scope/state invalid') END;
 SELECT CASE WHEN EXISTS(WITH RECURSIVE lineage(id,parent,depth) AS (
  SELECT id,parent_knowledge_id,1 FROM knowledges WHERE id=NEW.parent_knowledge_id
  UNION ALL SELECT k.id,k.parent_knowledge_id,l.depth+1 FROM knowledges k JOIN lineage l ON k.id=l.parent WHERE l.depth<=32
 ) SELECT 1 FROM lineage WHERE id=NEW.id OR depth>32) THEN RAISE(ABORT,'knowledge parent cycle/depth limit') END;
END;
CREATE TRIGGER knowledge_native_parent_scope BEFORE UPDATE OF tenant_id,knowledge_base_id ON knowledges
 WHEN (NEW.tenant_id != OLD.tenant_id OR NEW.knowledge_base_id != OLD.knowledge_base_id)
 AND (OLD.parent_knowledge_id IS NOT NULL OR EXISTS(SELECT 1 FROM knowledges WHERE parent_knowledge_id=OLD.id AND deleted_at IS NULL))
BEGIN SELECT RAISE(ABORT,'cross-KB move of linked knowledge is not supported'); END;
CREATE TRIGGER knowledge_native_parent_delete AFTER UPDATE OF deleted_at ON knowledges
 WHEN NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL
BEGIN UPDATE knowledges SET parent_knowledge_id=NULL,updated_at=CURRENT_TIMESTAMP
 WHERE tenant_id=NEW.tenant_id AND knowledge_base_id=NEW.knowledge_base_id AND (id=NEW.id OR parent_knowledge_id=NEW.id); END;
CREATE TRIGGER knowledge_native_parent_hard_delete AFTER DELETE ON knowledges
BEGIN UPDATE knowledges SET parent_knowledge_id=NULL,updated_at=CURRENT_TIMESTAMP
 WHERE tenant_id=OLD.tenant_id AND knowledge_base_id=OLD.knowledge_base_id AND parent_knowledge_id=OLD.id; END;

CREATE TRIGGER knowledge_native_deleting BEFORE UPDATE OF parse_status ON knowledges
 WHEN (OLD.deletion_pending OR OLD.parse_status='deleting') AND NEW.parse_status NOT IN ('deleting','failed','cancelled') AND NEW.deleted_at IS NULL
BEGIN SELECT RAISE(ABORT,'knowledge deletion is in progress'); END;
CREATE TRIGGER knowledge_native_deleting_mark AFTER UPDATE OF parse_status ON knowledges WHEN NEW.parse_status='deleting'
BEGIN UPDATE knowledges SET deletion_pending=TRUE WHERE id=NEW.id; END;
CREATE TRIGGER knowledge_native_deleting_insert AFTER INSERT ON knowledges WHEN NEW.parse_status='deleting'
BEGIN UPDATE knowledges SET deletion_pending=TRUE WHERE id=NEW.id; END;
CREATE TRIGGER knowledge_native_deleting_fence BEFORE UPDATE OF deletion_pending ON knowledges WHEN OLD.deletion_pending AND NOT NEW.deletion_pending
BEGIN SELECT RAISE(ABORT,'knowledge deletion is in progress'); END;
