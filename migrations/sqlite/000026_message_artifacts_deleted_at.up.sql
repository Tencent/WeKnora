-- Mirrors versioned migration 000107_message_artifacts_deleted_at: user-deleted
-- artifacts become tombstones so positions stay stable and the collector does
-- not re-attach them from the sandbox.
ALTER TABLE message_artifacts ADD COLUMN deleted_at DATETIME;

CREATE INDEX IF NOT EXISTS idx_message_artifacts_session_live
    ON message_artifacts (session_id, created_at)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_message_artifacts_url_live
    ON message_artifacts (url)
    WHERE deleted_at IS NULL;
