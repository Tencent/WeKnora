ALTER TABLE knowledges ADD COLUMN parent_knowledge_id VARCHAR(36) REFERENCES knowledges(id) ON DELETE SET NULL;
-- Internal fence survives terminal failure/recovery without changing the public API.
ALTER TABLE knowledges ADD COLUMN deletion_pending BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX idx_knowledge_parent ON knowledges(parent_knowledge_id) WHERE deleted_at IS NULL;
CREATE FUNCTION validate_native_knowledge_parent() RETURNS trigger AS $$
DECLARE found_parent VARCHAR(36);
BEGIN
 IF TG_OP = 'UPDATE' THEN
  IF (NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.knowledge_base_id IS DISTINCT FROM OLD.knowledge_base_id)
   AND (OLD.parent_knowledge_id IS NOT NULL OR EXISTS(SELECT 1 FROM knowledges WHERE parent_knowledge_id=OLD.id AND deleted_at IS NULL)) THEN
   RAISE EXCEPTION 'cross-KB move of linked knowledge is not supported' USING ERRCODE='23514';
  END IF;
  IF NEW.parent_knowledge_id IS NOT DISTINCT FROM OLD.parent_knowledge_id THEN RETURN NEW; END IF;
 END IF;
 IF NEW.parent_knowledge_id IS NULL THEN RETURN NEW; END IF;
 IF NEW.parent_knowledge_id=NEW.id THEN RAISE EXCEPTION 'knowledge parent cannot be itself' USING ERRCODE='23514'; END IF;
 SELECT id INTO found_parent FROM knowledges WHERE id=NEW.parent_knowledge_id AND tenant_id=NEW.tenant_id
  AND knowledge_base_id=NEW.knowledge_base_id AND deleted_at IS NULL AND parse_status != 'deleting' AND NOT deletion_pending FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'parent knowledge scope/state invalid' USING ERRCODE='23514'; END IF;
 IF EXISTS(WITH RECURSIVE lineage(id,parent,depth) AS (
  SELECT id,parent_knowledge_id,1 FROM knowledges WHERE id=NEW.parent_knowledge_id
  UNION ALL SELECT k.id,k.parent_knowledge_id,l.depth+1 FROM knowledges k JOIN lineage l ON k.id=l.parent WHERE l.depth<=32
 ) SELECT 1 FROM lineage WHERE id=NEW.id OR depth>32) THEN
  RAISE EXCEPTION 'knowledge parent cycle/depth limit' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER knowledge_native_parent BEFORE INSERT OR UPDATE OF parent_knowledge_id,tenant_id,knowledge_base_id ON knowledges
 FOR EACH ROW EXECUTE FUNCTION validate_native_knowledge_parent();
CREATE FUNCTION detach_deleted_knowledge_children() RETURNS trigger AS $$
BEGIN
 IF NEW.deleted_at IS NOT NULL AND OLD.deleted_at IS NULL THEN
  UPDATE knowledges SET parent_knowledge_id=NULL,updated_at=CURRENT_TIMESTAMP
   WHERE tenant_id=NEW.tenant_id AND knowledge_base_id=NEW.knowledge_base_id AND (id=NEW.id OR parent_knowledge_id=NEW.id);
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER knowledge_native_parent_delete AFTER UPDATE OF deleted_at ON knowledges
 FOR EACH ROW EXECUTE FUNCTION detach_deleted_knowledge_children();

-- Async parsing must not reopen a node after cascade admission marked it deleting.
CREATE FUNCTION preserve_knowledge_deleting_state() RETURNS trigger AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF (OLD.deletion_pending OR OLD.parse_status='deleting') AND NEW.parse_status NOT IN ('deleting','failed','cancelled') AND NEW.deleted_at IS NULL THEN
   RAISE EXCEPTION 'knowledge deletion is in progress' USING ERRCODE='23514';
  END IF;
  NEW.deletion_pending := OLD.deletion_pending OR NEW.deletion_pending;
 END IF;
 IF NEW.parse_status='deleting' THEN NEW.deletion_pending := TRUE; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER knowledge_native_deleting BEFORE INSERT OR UPDATE OF parse_status,deletion_pending ON knowledges FOR EACH ROW EXECUTE FUNCTION preserve_knowledge_deleting_state();
