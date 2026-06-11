ALTER TABLE embed_channels
    ADD COLUMN IF NOT EXISTS knowledge_base_id VARCHAR(36) DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_embed_channels_kb ON embed_channels (knowledge_base_id);
