-- Migration: 000063_knowledge_multi_tags
-- Description: Add multi-tag support for document knowledge via a join table.
DO $$ BEGIN RAISE NOTICE '[Migration 000063] Creating knowledge_tag_relations and migrating data...'; END $$;

-- Create the many-to-many join table
CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id        VARCHAR(36) NOT NULL,
    created_at    TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (knowledge_id, tag_id)
);

-- Index: find all tag IDs for a given knowledge
CREATE INDEX IF NOT EXISTS idx_ktr_knowledge
    ON knowledge_tag_relations(knowledge_id);

-- Index: find all knowledge IDs for a given tag
CREATE INDEX IF NOT EXISTS idx_ktr_tag
    ON knowledge_tag_relations(tag_id);

-- Migrate existing single-tag data into the join table
INSERT INTO knowledge_tag_relations (knowledge_id, tag_id, created_at)
SELECT id, tag_id, updated_at
FROM knowledges
WHERE tag_id IS NOT NULL AND tag_id != '';

-- Drop the old index and column
DROP INDEX IF EXISTS idx_knowledges_tag;
ALTER TABLE knowledges DROP COLUMN IF EXISTS tag_id;

DO $$ BEGIN RAISE NOTICE '[Migration 000063] knowledge_tag_relations ready'; END $$;
