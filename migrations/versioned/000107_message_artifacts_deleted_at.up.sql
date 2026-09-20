-- Migration: 000107_message_artifacts_deleted_at
-- Users can now delete a generated file from the artifact library or the
-- in-chat artifact panel. The row is kept as a tombstone rather than removed:
--
--   * message_artifacts.position is the address the download endpoint uses
--     (msg.Artifacts[index]); physically removing a middle row would shift
--     every later file's index and hand old links the wrong blob.
--   * ArtifactCollector de-duplicates sandbox files against the rows of the
--     session. Dropping the row would let the next collect re-attach the very
--     file the user just deleted, because its sandbox mtime has not moved.
--
-- The blob itself IS reclaimed (once no other owner still binds the resource),
-- so url stays on the tombstone only as a handle for a later GC retry.
DO $$ BEGIN RAISE NOTICE '[Migration 000107] Adding message_artifacts.deleted_at'; END $$;

ALTER TABLE message_artifacts
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP WITH TIME ZONE;

-- The artifact library and both list endpoints read live rows only; the
-- partial index keeps those scans off the tombstones.
CREATE INDEX IF NOT EXISTS idx_message_artifacts_session_live
    ON message_artifacts (session_id, created_at)
    WHERE deleted_at IS NULL;

-- Before a delete reclaims an object's bytes it checks whether any live row
-- anywhere still points at the same url (a forked session's copied rows, a
-- later answer that re-attached the file). That lookup is by url alone.
CREATE INDEX IF NOT EXISTS idx_message_artifacts_url_live
    ON message_artifacts (url)
    WHERE deleted_at IS NULL;

COMMENT ON COLUMN message_artifacts.deleted_at IS 'Set when the user deleted the file; the row survives to keep position stable and to stop the collector re-attaching it';
