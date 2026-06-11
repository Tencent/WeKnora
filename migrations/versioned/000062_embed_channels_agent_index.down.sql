DROP INDEX IF EXISTS idx_embed_channels_agent;

ALTER TABLE embed_channels ALTER COLUMN knowledge_base_id SET NOT NULL;
