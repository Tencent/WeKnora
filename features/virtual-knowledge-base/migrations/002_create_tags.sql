-- Migration 002: create tags table
CREATE TABLE IF NOT EXISTS tags (
    id SERIAL PRIMARY KEY,
    category_id INTEGER NOT NULL REFERENCES tag_categories(id) ON DELETE CASCADE,
    name VARCHAR(128) NOT NULL,
    value VARCHAR(128) NOT NULL,
    weight DOUBLE PRECISION DEFAULT 1.0,
    description TEXT,
    created_by BIGINT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE (category_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tags_category
    ON tags (category_id);
