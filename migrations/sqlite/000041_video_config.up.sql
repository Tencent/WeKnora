-- Video parsing configuration (see versioned/000124).
-- audio_only transcribes the audio track; the default leaves every existing
-- knowledge base archiving videos exactly as before.
ALTER TABLE knowledge_bases ADD COLUMN video_config TEXT
    DEFAULT '{"enabled": false, "mode": "audio_only"}';
