-- Add is_fallback column to messages table
-- Tracks whether a response was generated using fallback logic (no knowledge base match found)
ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_fallback BOOLEAN DEFAULT FALSE;
