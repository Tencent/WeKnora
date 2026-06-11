-- Migration: 000062_embed_channels_agent_index
-- Agent-scoped embed channels: relax legacy KB column, index by agent_id.
DO $$ BEGIN RAISE NOTICE '[Migration 000062] Updating embed_channels for agent-scoped lookup'; END $$;

ALTER TABLE embed_channels ALTER COLUMN knowledge_base_id DROP NOT NULL;
ALTER TABLE embed_channels ALTER COLUMN knowledge_base_id SET DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_embed_channels_agent ON embed_channels (agent_id);

COMMENT ON COLUMN embed_channels.knowledge_base_id IS 'Deprecated: legacy denormalized KB; runtime KB scope comes from agent config';

DO $$ BEGIN RAISE NOTICE '[Migration 000062] embed_channels agent index created'; END $$;
