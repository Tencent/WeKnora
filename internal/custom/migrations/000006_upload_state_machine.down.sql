DROP INDEX IF EXISTS idx_videos_uploading_updated_at;

ALTER TABLE videos
    DROP COLUMN IF EXISTS upload_object_key;
