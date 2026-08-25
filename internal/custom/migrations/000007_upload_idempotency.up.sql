ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS upload_idempotency_key VARCHAR(128);

CREATE UNIQUE INDEX IF NOT EXISTS idx_videos_upload_idempotency_key
    ON videos(upload_idempotency_key)
    WHERE upload_idempotency_key IS NOT NULL AND upload_idempotency_key <> '';
