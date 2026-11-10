-- Rollback: 000027_message_rendered_content
ALTER TABLE messages DROP COLUMN IF EXISTS rendered_content;
