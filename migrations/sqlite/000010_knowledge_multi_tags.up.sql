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
