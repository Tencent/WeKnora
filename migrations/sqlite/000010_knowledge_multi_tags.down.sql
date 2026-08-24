ALTER TABLE knowledges ADD COLUMN tag_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_knowledges_tag ON knowledges(tag_id);

UPDATE knowledges
SET tag_id = (
    SELECT ktr.tag_id
    FROM knowledge_tag_relations ktr
    WHERE ktr.knowledge_id = knowledges.id
    ORDER BY ktr.created_at ASC
    LIMIT 1
)
WHERE EXISTS (
    SELECT 1 FROM knowledge_tag_relations ktr WHERE ktr.knowledge_id = knowledges.id
);

DROP INDEX IF EXISTS idx_ktr_tag;
DROP INDEX IF EXISTS idx_ktr_knowledge;
DROP TABLE IF EXISTS knowledge_tag_relations;
