-- Migration 000010: Add seq_id (auto-increment integer ID) to chunks and knowledge_tags tables
-- This provides integer IDs for FAQ entries and tags for external API usage

-- ============================================================================
-- Section 1: Add seq_id to chunks table
-- ============================================================================

DO $$ BEGIN RAISE NOTICE '[Migration 000010] Adding seq_id column to chunks table'; END $$;

-- Create sequence for chunks with starting value > 72528124
CREATE SEQUENCE IF NOT EXISTS chunks_seq_id_seq START WITH 100000000;

-- Add seq_id column to chunks table
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS seq_id BIGINT;

-- Set default value using sequence
ALTER TABLE chunks ALTER COLUMN seq_id SET DEFAULT nextval('chunks_seq_id_seq');

-- Populate existing rows with sequence values
UPDATE chunks SET seq_id = nextval('chunks_seq_id_seq') WHERE seq_id IS NULL;

-- Make seq_id NOT NULL after populating
ALTER TABLE chunks ALTER COLUMN seq_id SET NOT NULL;

-- Create unique index on seq_id
CREATE UNIQUE INDEX IF NOT EXISTS idx_chunks_seq_id ON chunks(seq_id);

-- ============================================================================
-- Section 2: Add seq_id to knowledge_tags table
-- ============================================================================

DO $$ BEGIN RAISE NOTICE '[Migration 000010] Adding seq_id column to knowledge_tags table'; END $$;

-- Create sequence for knowledge_tags with starting value > 2924026
CREATE SEQUENCE IF NOT EXISTS knowledge_tags_seq_id_seq START WITH 10000000;

-- Add seq_id column to knowledge_tags table
ALTER TABLE knowledge_tags ADD COLUMN IF NOT EXISTS seq_id BIGINT;

-- Set default value using sequence
ALTER TABLE knowledge_tags ALTER COLUMN seq_id SET DEFAULT nextval('knowledge_tags_seq_id_seq');

-- Populate existing rows with sequence values
UPDATE knowledge_tags SET seq_id = nextval('knowledge_tags_seq_id_seq') WHERE seq_id IS NULL;

-- Make seq_id NOT NULL after populating
ALTER TABLE knowledge_tags ALTER COLUMN seq_id SET NOT NULL;

-- Create unique index on seq_id
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_tags_seq_id ON knowledge_tags(seq_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000010] seq_id columns added successfully!'; END $$;
