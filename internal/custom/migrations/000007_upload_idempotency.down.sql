DROP INDEX IF EXISTS idx_videos_upload_idempotency_key;
ALTER TABLE videos DROP COLUMN IF EXISTS upload_idempotency_key;
