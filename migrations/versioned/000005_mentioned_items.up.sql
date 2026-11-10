-- Add mentioned_items column to messages table
-- This column stores @mentioned knowledge bases and files when user sends a message

ALTER TABLE messages ADD COLUMN IF NOT EXISTS mentioned_items JSONB DEFAULT '[]';

-- Add comment for the column
COMMENT ON COLUMN messages.mentioned_items IS 'Stores @mentioned knowledge bases and files (id, name, type) when user sends a message';
