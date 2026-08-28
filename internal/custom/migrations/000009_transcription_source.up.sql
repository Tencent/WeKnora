ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS transcription_source_url TEXT;
