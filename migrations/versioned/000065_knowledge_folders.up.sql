-- Migration 000065: Knowledge Folders
-- Purpose: Add hierarchical folder support to knowledge bases

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Creating knowledge_folders table...'; END $$;

CREATE TABLE IF NOT EXISTS knowledge_folders (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    parent_folder_id VARCHAR(36),
    path TEXT NOT NULL,
    depth INTEGER NOT NULL DEFAULT 0,
    sort_order INTEGER DEFAULT 0,
    color VARCHAR(32),
    description TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,

    CONSTRAINT fk_knowledge_folders_kb
        FOREIGN KEY (knowledge_base_id) REFERENCES knowledge_bases(id)
        ON DELETE CASCADE,
    CONSTRAINT fk_knowledge_folders_parent
        FOREIGN KEY (parent_folder_id) REFERENCES knowledge_folders(id)
        ON DELETE CASCADE
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_folders_tenant_kb ON knowledge_folders(tenant_id, knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_folders_parent ON knowledge_folders(parent_folder_id);
CREATE INDEX IF NOT EXISTS idx_folders_path ON knowledge_folders(path);
CREATE UNIQUE INDEX IF NOT EXISTS idx_folders_unique_name
    ON knowledge_folders(knowledge_base_id, COALESCE(parent_folder_id, '00000000-0000-0000-0000-000000000000'), name)
    WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Adding folder_id column to knowledges...'; END $$;

ALTER TABLE knowledges
    ADD COLUMN IF NOT EXISTS folder_id VARCHAR(36);

ALTER TABLE knowledges
    ADD CONSTRAINT fk_knowledge_folder
        FOREIGN KEY (folder_id) REFERENCES knowledge_folders(id)
        ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_knowledges_folder ON knowledges(folder_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000065] Knowledge folders migration complete'; END $$;
