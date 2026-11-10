-- Add knowledge_base_id column to im_channels table.
-- When set, file messages received on this channel will be saved to the specified knowledge base.
ALTER TABLE im_channels ADD COLUMN IF NOT EXISTS knowledge_base_id VARCHAR(36) DEFAULT '';
