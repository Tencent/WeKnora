-- Per-session extraction progress and deferred proposal replacement.
ALTER TABLE memory_subjects ADD COLUMN extraction_state TEXT;
ALTER TABLE memory_items ADD COLUMN replaces_id VARCHAR(36) NOT NULL DEFAULT '';

-- Recover the old active record when a legacy pending inference prematurely
-- superseded it. Keep an explicit target so confirmation can still replace it.
UPDATE memory_items
SET replaces_id = (
    SELECT old.id FROM memory_items AS old
    WHERE old.tenant_id = memory_items.tenant_id
      AND old.subject_id = memory_items.subject_id
      AND old.superseded_by = memory_items.id AND old.status = 'superseded'
    ORDER BY old.valid_from DESC, old.id DESC LIMIT 1
)
WHERE status = 'pending' AND EXISTS (
    SELECT 1 FROM memory_items AS old
    WHERE old.tenant_id = memory_items.tenant_id
      AND old.subject_id = memory_items.subject_id
      AND old.superseded_by = memory_items.id AND old.status = 'superseded'
);

-- Restore at most one record per key, and never displace a newer active fact.
UPDATE memory_items
SET status = 'active', invalid_at = NULL, superseded_by = ''
WHERE status = 'superseded'
  AND NOT EXISTS (
    SELECT 1 FROM memory_items AS active
    WHERE active.tenant_id = memory_items.tenant_id
      AND active.subject_id = memory_items.subject_id
      AND active.normalized_key = memory_items.normalized_key AND active.status = 'active'
  )
  AND id = (
    SELECT old.id FROM memory_items AS old
    WHERE old.tenant_id = memory_items.tenant_id
      AND old.subject_id = memory_items.subject_id
      AND old.normalized_key = memory_items.normalized_key AND old.status = 'superseded'
      AND EXISTS (
        SELECT 1 FROM memory_items AS pending
        WHERE pending.tenant_id = old.tenant_id AND pending.subject_id = old.subject_id
          AND pending.status = 'pending' AND pending.replaces_id = old.id
      )
    ORDER BY old.valid_from DESC, old.id DESC LIMIT 1
  );
