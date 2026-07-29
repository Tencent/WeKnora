-- SQLite migration: structured wiki blocks with paragraph-level sources.

-- The shared Wiki publication guard compares every positive source attempt
-- with the latest persisted parsing attempt. PostgreSQL has carried this
-- table since versioned migration 000055, but the SQLite baseline omitted it.
-- Create the full Lite-compatible shape here so provenance publication and
-- the existing span tracker have the same source of truth. IF NOT EXISTS
-- keeps installations that already repaired the omission untouched.
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id        VARCHAR(64) NOT NULL,
    attempt             INTEGER NOT NULL DEFAULT 1,
    span_id             VARCHAR(64) NOT NULL,
    parent_span_id      VARCHAR(64),
    name                VARCHAR(255) NOT NULL,
    kind                VARCHAR(16) NOT NULL,
    status              VARCHAR(16) NOT NULL,
    input               TEXT,
    output              TEXT,
    metadata            TEXT,
    error_code          VARCHAR(64),
    error_message       TEXT,
    error_detail        TEXT,
    started_at          DATETIME,
    finished_at         DATETIME,
    duration_ms         INTEGER,
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_kpspan_attempt_span UNIQUE (knowledge_id, attempt, span_id)
);

CREATE INDEX IF NOT EXISTS idx_kpspan_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);

CREATE INDEX IF NOT EXISTS idx_kpspan_status_started
    ON knowledge_processing_spans (status, started_at);

CREATE INDEX IF NOT EXISTS idx_kpspan_parent
    ON knowledge_processing_spans (parent_span_id)
    WHERE parent_span_id IS NOT NULL;

ALTER TABLE wiki_pages
    ADD COLUMN current_block_set_id VARCHAR(36) NOT NULL DEFAULT '';

ALTER TABLE wiki_page_revisions
    ADD COLUMN block_set_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_wiki_pages_current_block_set
    ON wiki_pages (current_block_set_id)
    WHERE current_block_set_id <> '';

CREATE INDEX IF NOT EXISTS idx_wiki_page_revisions_block_set
    ON wiki_page_revisions (block_set_id)
    WHERE block_set_id <> '';

CREATE TABLE IF NOT EXISTS wiki_page_block_sets (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           INTEGER NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    page_id             VARCHAR(36) NOT NULL,
    page_version        INTEGER NOT NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'staged',
    rendered_content    TEXT NOT NULL DEFAULT '',
    rendered_summary    TEXT NOT NULL DEFAULT '',
    generation_run_id   VARCHAR(64) NOT NULL DEFAULT '',
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at        DATETIME
);

-- Staged attempts may coexist; optimistic page publication selects one while
-- the partial unique index permits only one finalized snapshot per version.
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
    section_path        TEXT NOT NULL DEFAULT '[]',
    sort_order          INTEGER NOT NULL DEFAULT 0,
    content             TEXT NOT NULL DEFAULT '',
    content_hash        VARCHAR(64) NOT NULL DEFAULT '',
    author_type         VARCHAR(16) NOT NULL DEFAULT 'pipeline',
    provenance_status   VARCHAR(24) NOT NULL DEFAULT 'unsupported',
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_blocks_set_order
    ON wiki_page_blocks (block_set_id, sort_order);

CREATE INDEX IF NOT EXISTS idx_wiki_blocks_logical_id
    ON wiki_page_blocks (logical_block_id);

CREATE TABLE IF NOT EXISTS wiki_block_sources (
    id                  VARCHAR(36) PRIMARY KEY,
    tenant_id           INTEGER NOT NULL,
    knowledge_base_id   VARCHAR(36) NOT NULL,
    block_id            VARCHAR(36) NOT NULL,
    knowledge_id        VARCHAR(36) NOT NULL,
    source_title        VARCHAR(512) NOT NULL DEFAULT '',
    knowledge_attempt   INTEGER NOT NULL DEFAULT 0,
    chunk_id            VARCHAR(36) NOT NULL,
    chunk_revision      INTEGER NOT NULL DEFAULT 0,
    sort_order          INTEGER NOT NULL DEFAULT 0,
    evidence            TEXT NOT NULL DEFAULT '',
    evidence_hash       VARCHAR(64) NOT NULL DEFAULT '',
    chunk_content_hash  VARCHAR(64) NOT NULL DEFAULT '',
    validation_status   VARCHAR(16) NOT NULL DEFAULT 'invalid',
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_wiki_block_sources_evidence
    ON wiki_block_sources (block_id, chunk_id, evidence_hash);

CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_block
    ON wiki_block_sources (block_id, sort_order);

CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_knowledge
    ON wiki_block_sources (knowledge_base_id, knowledge_id, block_id);

CREATE INDEX IF NOT EXISTS idx_wiki_block_sources_chunk
    ON wiki_block_sources (chunk_id);
