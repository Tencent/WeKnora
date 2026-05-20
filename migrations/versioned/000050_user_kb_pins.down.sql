-- Rollback: drop per-user KB pin mapping. The tenant-wide is_pinned /
-- pinned_at columns on knowledge_bases were not modified by the up
-- migration and continue to drive the legacy pin behaviour after this
-- rollback runs.
DO $$ BEGIN RAISE NOTICE '[Migration 000050] Rollback: dropping user_kb_pins'; END $$;

DROP INDEX IF EXISTS idx_user_kb_pins_user_tenant_pinned_at;
DROP TABLE IF EXISTS user_kb_pins;
