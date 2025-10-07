-- Migration 003: create document_tags table
CREATE TABLE IF NOT EXISTS document_tags (
    id SERIAL PRIMARY KEY,
    document_id UUID NOT NULL,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    weight DOUBLE PRECISION,
    created_by BIGINT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE (document_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_document_tags_document
    ON document_tags (document_id);

CREATE INDEX IF NOT EXISTS idx_document_tags_tag
    ON document_tags (tag_id);
