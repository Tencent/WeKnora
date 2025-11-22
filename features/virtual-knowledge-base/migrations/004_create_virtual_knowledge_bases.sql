-- Migration 004: create virtual_knowledge_bases table
CREATE TABLE IF NOT EXISTS virtual_knowledge_bases (
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    description TEXT,
    created_by BIGINT,
    config JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE (name)
);
