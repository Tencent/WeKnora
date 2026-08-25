ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS upload_object_key TEXT;

CREATE INDEX IF NOT EXISTS idx_videos_uploading_updated_at
    ON videos(status, updated_at);
