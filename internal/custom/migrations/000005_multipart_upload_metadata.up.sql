ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS upload_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS upload_size_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS upload_part_size_bytes BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_videos_upload_id ON videos(upload_id);
