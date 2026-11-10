-- Mirrors versioned migration 000063_knowledge_multi_tags:
-- many-to-many join table between knowledges and knowledge_tags.

CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id       VARCHAR(36) NOT NULL,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_ktr_knowledge
    ON knowledge_tag_relations (knowledge_id);

CREATE INDEX IF NOT EXISTS idx_ktr_tag
    ON knowledge_tag_relations (tag_id);

-- Migrate existing single-tag data into the join table.
INSERT INTO knowledge_tag_relations (knowledge_id, tag_id, created_at)
SELECT id, tag_id, updated_at
FROM knowledges
WHERE tag_id IS NOT NULL AND tag_id != ''
  AND deleted_at IS NULL;

DROP INDEX IF EXISTS idx_knowledges_tag;
ALTER TABLE knowledges DROP COLUMN tag_id;
