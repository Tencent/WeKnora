-- Migration: 000063_knowledge_multi_tags (rollback)
-- Description: Restore single tag_id column and drop the join table.
DO $$ BEGIN RAISE NOTICE '[Migration 000063 rollback] Restoring knowledges.tag_id from knowledge_tag_relations...'; END $$;

-- Restore the tag_id column
ALTER TABLE knowledges ADD COLUMN IF NOT EXISTS tag_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_knowledges_tag ON knowledges(tag_id);

-- Restore the first tag (by created_at) from the join table back to knowledges
UPDATE knowledges k
SET tag_id = (
    SELECT ktr.tag_id
    FROM knowledge_tag_relations ktr
    WHERE ktr.knowledge_id = k.id
    ORDER BY ktr.created_at ASC
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1 FROM knowledge_tag_relations ktr WHERE ktr.knowledge_id = k.id
);

-- Drop the join table
DROP TABLE IF EXISTS knowledge_tag_relations;

DO $$ BEGIN RAISE NOTICE '[Migration 000063 rollback] knowledges.tag_id restored'; END $$;
