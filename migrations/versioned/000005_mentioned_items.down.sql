-- Remove mentioned_items column from messages table

ALTER TABLE messages DROP COLUMN IF EXISTS mentioned_items;
