-- Migration: 000075_wiki_page_revisions
-- Description: Wiki page revision history (content snapshots for diff/rollback)
-- plus edit provenance columns on wiki_pages.
DO $$ BEGIN RAISE NOTICE '[Migration 000075] Applying wiki page revisions schema'; END $$;

-- ---------------------------------------------------------------------------
-- 1) Edit provenance on wiki_pages
-- last_edit_source records who authored the CURRENT version of the page:
-- 'pipeline' (wiki ingest), 'agent' (wiki fixer tools), 'user' (manual edit
-- via the editor UI) or 'revert' (rollback to an earlier revision). Empty for
-- legacy rows, which are treated as 'pipeline'.
-- ---------------------------------------------------------------------------
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS last_edit_source VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS last_editor_id VARCHAR(64) NOT NULL DEFAULT '';

COMMENT ON COLUMN wiki_pages.last_edit_source IS 'Author kind of the current version: pipeline | agent | user | revert ('''' = legacy, treated as pipeline)';
COMMENT ON COLUMN wiki_pages.last_editor_id IS 'User id of the caller that produced the current version (empty for background pipeline writes)';

-- ---------------------------------------------------------------------------
-- 2) wiki_page_revisions table
-- One immutable snapshot per superseded page version. The CURRENT version
-- lives only in wiki_pages; when an edit replaces version V, the pre-edit
-- state is inserted here as (page_id, V) before the row is rewritten, so
-- every historical version stays diffable and revertable.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_page_revisions (
    id              VARCHAR(36) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    page_id         VARCHAR(36) NOT NULL,
    slug            VARCHAR(255) NOT NULL,
    version         INT NOT NULL,
    title           VARCHAR(512) NOT NULL DEFAULT '',
    page_type       VARCHAR(32) NOT NULL DEFAULT 'summary',
    status          VARCHAR(32) NOT NULL DEFAULT 'published',
    content         TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    aliases         JSONB DEFAULT '[]'::JSONB,
    -- Author of THIS version (mirrors wiki_pages.last_edit_source semantics).
    edit_source     VARCHAR(16) NOT NULL DEFAULT '',
    editor_id       VARCHAR(64) NOT NULL DEFAULT '',
    -- When this version was authored (the page's updated_at while current).
    edited_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Exactly one snapshot per page version; ON CONFLICT DO NOTHING keeps the
-- snapshot-then-update write path idempotent under retries.
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_page_revisions_page_version
    ON wiki_page_revisions (page_id, version);

CREATE INDEX IF NOT EXISTS idx_wiki_page_revisions_kb_slug
    ON wiki_page_revisions (knowledge_base_id, slug);
