-- Migration: 000037_wiki_and_indexing
-- Description: Wiki feature schema (wiki_pages table + wiki_config column + wiki_page_issues table)
-- and the indexing_strategy column on knowledge_bases.
DO $$ BEGIN RAISE NOTICE '[Migration 000037] Applying wiki + indexing schema'; END $$;

-- ---------------------------------------------------------------------------
-- 1) wiki_pages table and wiki_config column
-- Wiki pages are LLM-generated, interlinked markdown documents that form a
-- persistent wiki for a knowledge base.
-- ---------------------------------------------------------------------------
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS wiki_config JSONB;

COMMENT ON COLUMN knowledge_bases.wiki_config IS 'Wiki configuration: {"auto_ingest": bool, "synthesis_model_id": string, "wiki_language": string, "max_pages_per_ingest": int}';

CREATE TABLE IF NOT EXISTS wiki_pages (
    id              VARCHAR(36) PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug            VARCHAR(255) NOT NULL,
    title           VARCHAR(512) NOT NULL DEFAULT '',
    page_type       VARCHAR(32) NOT NULL DEFAULT 'summary',
    status          VARCHAR(32) NOT NULL DEFAULT 'published',
    content         TEXT NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    source_refs     JSONB DEFAULT '[]'::JSONB,
    chunk_refs      JSONB DEFAULT '[]'::JSONB,
    in_links        JSONB DEFAULT '[]'::JSONB,
    out_links       JSONB DEFAULT '[]'::JSONB,
    page_metadata   JSONB DEFAULT '{}'::JSONB,
    aliases         JSONB DEFAULT '[]'::JSONB,
    version         INT NOT NULL DEFAULT 1,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMP WITH TIME ZONE
);

-- slug must be unique within a knowledge base (for non-deleted pages)
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_pages_kb_slug
    ON wiki_pages (knowledge_base_id, slug)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_wiki_pages_kb_id
    ON wiki_pages (knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_wiki_pages_page_type
    ON wiki_pages (knowledge_base_id, page_type);

CREATE INDEX IF NOT EXISTS idx_wiki_pages_tenant_id
    ON wiki_pages (tenant_id);

CREATE INDEX IF NOT EXISTS idx_wiki_pages_deleted_at
    ON wiki_pages (deleted_at);

CREATE INDEX IF NOT EXISTS idx_wiki_pages_fulltext
    ON wiki_pages USING GIN (to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')));

-- ---------------------------------------------------------------------------
-- 2) wiki_page_issues table
-- Reports issues against generated wiki pages (LLM-flagged or user-flagged).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS wiki_page_issues (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    slug VARCHAR(255) NOT NULL,
    issue_type VARCHAR(50) NOT NULL,
    description TEXT NOT NULL,
    suspected_knowledge_ids JSONB,
    status VARCHAR(20) DEFAULT 'pending' NOT NULL,
    reported_by VARCHAR(100) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_tenant_id ON wiki_page_issues(tenant_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_knowledge_base_id ON wiki_page_issues(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_slug ON wiki_page_issues(slug);
CREATE INDEX IF NOT EXISTS idx_wiki_page_issues_status ON wiki_page_issues(status);

-- ---------------------------------------------------------------------------
-- 3) indexing_strategy column on knowledge_bases
-- Controls which indexing pipelines are active (vector, keyword, wiki, graph).
-- Backfill: existing rows get vector+keyword=true (legacy default behavior);
-- wiki_enabled / graph_enabled stay false until explicitly enabled.
-- ---------------------------------------------------------------------------
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS indexing_strategy JSONB;

COMMENT ON COLUMN knowledge_bases.indexing_strategy IS 'Indexing pipelines strategy: {"vector_enabled": bool, "keyword_enabled": bool, "wiki_enabled": bool, "graph_enabled": bool}';

UPDATE knowledge_bases
SET indexing_strategy = jsonb_build_object(
    'vector_enabled',  TRUE,
    'keyword_enabled', TRUE,
    'wiki_enabled',    FALSE,
    'graph_enabled',   FALSE
)
WHERE indexing_strategy IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000037] wiki + indexing schema applied successfully'; END $$;
