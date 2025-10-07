-- Migration 005: create virtual_kb_filters table
CREATE TABLE IF NOT EXISTS virtual_kb_filters (
    id SERIAL PRIMARY KEY,
    virtual_kb_id INTEGER NOT NULL REFERENCES virtual_knowledge_bases(id) ON DELETE CASCADE,
    tag_category_id INTEGER NOT NULL REFERENCES tag_categories(id) ON DELETE CASCADE,
    operator VARCHAR(16) NOT NULL DEFAULT 'OR',
    weight DOUBLE PRECISION DEFAULT 1.0,
    tag_ids INTEGER[] NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_virtual_kb_filters_vkb
    ON virtual_kb_filters (virtual_kb_id);
