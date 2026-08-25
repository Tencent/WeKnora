DROP INDEX IF EXISTS idx_videos_upload_id;

ALTER TABLE videos
    DROP COLUMN IF EXISTS upload_id,
    DROP COLUMN IF EXISTS upload_size_bytes,
    DROP COLUMN IF EXISTS upload_part_size_bytes;
