-- Remove is_fallback column from messages table
ALTER TABLE messages DROP COLUMN IF EXISTS is_fallback;
