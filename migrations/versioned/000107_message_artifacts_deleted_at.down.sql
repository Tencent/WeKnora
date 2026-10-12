-- Reverting drops the tombstone marker, which resurrects every deleted file in
-- the listings. The blobs of hard-deleted artifacts are already gone, so those
-- rows will 404 on download until they are cleaned up.
DROP INDEX IF EXISTS idx_message_artifacts_url_live;
DROP INDEX IF EXISTS idx_message_artifacts_session_live;

ALTER TABLE message_artifacts DROP COLUMN IF EXISTS deleted_at;
