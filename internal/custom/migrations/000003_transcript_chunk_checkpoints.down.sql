DROP TABLE IF EXISTS video_transcript_chunks;
ALTER TABLE videos DROP COLUMN IF EXISTS transcript_generation;
ALTER TABLE videos DROP COLUMN IF EXISTS transcript_revision;
ALTER TABLE videos DROP COLUMN IF EXISTS transcript_active_revision;
