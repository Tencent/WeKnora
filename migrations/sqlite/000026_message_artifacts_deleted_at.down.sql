DROP INDEX IF EXISTS idx_message_artifacts_session_live;

ALTER TABLE message_artifacts DROP COLUMN deleted_at;
