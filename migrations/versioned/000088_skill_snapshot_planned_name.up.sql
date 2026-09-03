-- Description: The name a snapshot was going to be committed under, recorded
-- before the provider call. snapshot_id can only be filled in afterwards, so a
-- process that died between the commit and that write left a real provider
-- snapshot the ledger could not name — and therefore could never reclaim.
DO $$ BEGIN RAISE NOTICE '[Migration 000088] Adding planned_name to tenant_skill_snapshots'; END $$;

ALTER TABLE tenant_skill_snapshots ADD COLUMN IF NOT EXISTS planned_name VARCHAR(255);

COMMENT ON COLUMN tenant_skill_snapshots.planned_name IS 'Name passed to CreateSnapshot, written before the provider call so an abandoned build stays identifiable';
