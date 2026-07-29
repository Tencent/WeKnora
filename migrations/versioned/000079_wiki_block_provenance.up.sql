-- Migration: 000079_wiki_block_provenance
-- Structured, immutable wiki block sets with paragraph-level source evidence.

ALTER TABLE wiki_pages
    ADD COLUMN IF NOT EXISTS current_block_set_id VARCHAR(36) NOT NULL DEFAULT '';

ALTER TABLE wiki_page_revisions
    ADD COLUMN IF NOT EXISTS block_set_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_wiki_pages_current_block_set
    ON wiki_pages (current_block_set_id)
    WHERE current_block_set_id <> '';

CREATE INDEX IF NOT EXISTS idx_wiki_page_revisions_block_set
    ON wiki_page_revisions (block_set_id)
    WHERE block_set_id <> '';

-- Each positive parsing attempt may drain a named finalizing-counter slot only
-- once. The repository inserts this marker and decrements the counter in the
-- same transaction.
CREATE TABLE IF NOT EXISTS knowledge_subtask_settlements (
    id                  BIGSERIAL PRIMARY KEY,
    knowledge_id        VARCHAR(64) NOT NULL,
    attempt             INT NOT NULL,
    subtask_key         VARCHAR(255) NOT NULL,
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_knowledge_subtask_settlement
        UNIQUE (knowledge_id, attempt, subtask_key)
);

CREATE TABLE IF NOT EXISTS wiki_page_block_sets (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    page_id             VARCHAR(36) NOT NULL,
    page_version        INT NOT NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'staged',
    rendered_content    TEXT NOT NULL DEFAULT '',
    rendered_summary    TEXT NOT NULL DEFAULT '',
    generation_run_id   VARCHAR(64) NOT NULL DEFAULT '',
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    published_at        TIMESTAMP WITH TIME ZONE
);

-- Staged attempts may coexist for one target version, so a crashed stage does
-- not block a later retry. The page-version CAS selects the winner; this
-- partial unique index permits only one finalized snapshot for that version.
CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_block_sets_page_version
    ON wiki_page_block_sets (page_id, page_version)
    WHERE status = 'published' OR status = 'superseded';

CREATE INDEX IF NOT EXISTS idx_wiki_block_sets_kb_page_status
    ON wiki_page_block_sets (knowledge_base_id, page_id, status);

CREATE TABLE IF NOT EXISTS wiki_page_blocks (
    id                  VARCHAR(36) PRIMARY KEY,
    block_set_id        VARCHAR(36) NOT NULL,
    logical_block_id    VARCHAR(36) NOT NULL,
    block_type          VARCHAR(24) NOT NULL,
    section_path        JSONB NOT NULL DEFAULT '[]'::JSONB,
    sort_order          INT NOT NULL DEFAULT 0,
    content             TEXT NOT NULL DEFAULT '',
    content_hash        VARCHAR(64) NOT NULL DEFAULT '',
    author_type         VARCHAR(16) NOT NULL DEFAULT 'pipeline',
    provenance_status   VARCHAR(24) NOT NULL DEFAULT 'unsupported',
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_blocks_set_order
    ON wiki_page_blocks (block_set_id, sort_order);

CREATE INDEX IF NOT EXISTS idx_wiki_blocks_logical_id
    ON wiki_page_blocks (logical_block_id);

CREATE TABLE IF NOT EXISTS wiki_block_sources (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           BIGINT NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    block_id            VARCHAR(36) NOT NULL,
    knowledge_id        VARCHAR(36) NOT NULL,
    source_title        VARCHAR(512) NOT NULL DEFAULT '',
    knowledge_attempt   INT NOT NULL DEFAULT 0,
    chunk_id            VARCHAR(36) NOT NULL,
    chunk_revision      INT NOT NULL DEFAULT 0,
    sort_order          INT NOT NULL DEFAULT 0,
    evidence            TEXT NOT NULL DEFAULT '',
    evidence_hash       VARCHAR(64) NOT NULL DEFAULT '',
    chunk_content_hash  VARCHAR(64) NOT NULL DEFAULT '',
    validation_status   VARCHAR(16) NOT NULL DEFAULT 'invalid',
    created_at          TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_block_sources_evidence
    ON wiki_block_sources (block_id, chunk_id, evidence_hash);

CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_block
    ON wiki_block_sources (block_id, sort_order);

CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_knowledge
    ON wiki_block_sources (knowledge_base_id, knowledge_id, block_id);

CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_chunk
    ON wiki_block_sources (chunk_id);
