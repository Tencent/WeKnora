-- Migration 000112: knowledge guidance (personal knowledge state) ledgers.
--
-- Four behavior ledgers plus one daily aggregation bucket, all scoped by
-- (tenant_id, subject_id). They back the personal-knowledge-state overlay on
-- the Wiki graph, and are deliberately decoupled from memory_doc_affinity
-- (which the reranker keeps using): deleting a person's learning profile
-- therefore never disturbs existing retrieval personalization or long-term
-- memory. See the design doc "课题四" for the semantics of each ledger.

CREATE TABLE IF NOT EXISTS memory_citation_events (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_citation_event ON memory_citation_events (tenant_id, subject_id, message_id, knowledge_id);

CREATE TABLE IF NOT EXISTS memory_citations (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    cite_count        INTEGER NOT NULL DEFAULT 0,
    last_cited_at     TIMESTAMP WITH TIME ZONE,
    created_at        TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_citation_scope
    ON memory_citations (tenant_id, subject_id, knowledge_id);

CREATE TABLE IF NOT EXISTS memory_page_views (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    slug              VARCHAR(255) NOT NULL,
    view_count        INTEGER NOT NULL DEFAULT 0,
    total_duration    BIGINT NOT NULL DEFAULT 0,
    last_view_at      TIMESTAMP WITH TIME ZONE,
    created_at        TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at        TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_view_scope
    ON memory_page_views (tenant_id, subject_id, knowledge_base_id, slug);

CREATE TABLE IF NOT EXISTS memory_answer_likes (
    id            VARCHAR(36) PRIMARY KEY,
    tenant_id     BIGINT NOT NULL,
    subject_id    VARCHAR(512) NOT NULL,
    message_id    VARCHAR(36) NOT NULL,
    allocations   JSONB NOT NULL,
    liked_at      TIMESTAMP WITH TIME ZONE,
    cancelled_at  TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_like_scope
    ON memory_answer_likes (tenant_id, subject_id, message_id);

CREATE TABLE IF NOT EXISTS memory_guide_exposures (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    session_id        VARCHAR(36),
    trigger_slug      VARCHAR(255),
    candidate_slug    VARCHAR(255),
    strategy          VARCHAR(32),
    rank              INTEGER,
    shown_at          TIMESTAMP WITH TIME ZONE,
    clicked_at        TIMESTAMP WITH TIME ZONE,
    qualified_view_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_mastery_exposure_scope
    ON memory_guide_exposures (tenant_id, subject_id, shown_at);

CREATE TABLE IF NOT EXISTS memory_mastery_daily (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         BIGINT NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    slug              VARCHAR(255) NOT NULL,
    event_type        VARCHAR(16) NOT NULL,
    event_date        DATE NOT NULL,
    event_count       INTEGER NOT NULL DEFAULT 0,
    duration_sum      BIGINT NOT NULL DEFAULT 0,
    updated_at        TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_daily_scope
    ON memory_mastery_daily (tenant_id, subject_id, knowledge_base_id, slug, event_type, event_date);

