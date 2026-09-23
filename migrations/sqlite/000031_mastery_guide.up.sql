-- Knowledge guidance (personal knowledge state) ledgers (Lite).
-- Mirrors migrations/versioned/000111. JSONB maps to TEXT; timestamptz to DATETIME.

CREATE TABLE IF NOT EXISTS memory_citation_events (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_citation_event ON memory_citation_events (tenant_id, subject_id, message_id, knowledge_id);

CREATE TABLE IF NOT EXISTS memory_citations (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_id      VARCHAR(36) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    cite_count        INTEGER NOT NULL DEFAULT 0,
    last_cited_at     DATETIME,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_citation_scope
    ON memory_citations (tenant_id, subject_id, knowledge_id);

CREATE TABLE IF NOT EXISTS memory_page_views (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    slug              VARCHAR(255) NOT NULL,
    view_count        INTEGER NOT NULL DEFAULT 0,
    total_duration    INTEGER NOT NULL DEFAULT 0,
    last_view_at      DATETIME,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_view_scope
    ON memory_page_views (tenant_id, subject_id, knowledge_base_id, slug);

CREATE TABLE IF NOT EXISTS memory_answer_likes (
    id            VARCHAR(36) PRIMARY KEY,
    tenant_id     INTEGER NOT NULL,
    subject_id    VARCHAR(512) NOT NULL,
    message_id    VARCHAR(36) NOT NULL,
    allocations   TEXT NOT NULL,
    liked_at      DATETIME,
    cancelled_at  DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_like_scope
    ON memory_answer_likes (tenant_id, subject_id, message_id);

CREATE TABLE IF NOT EXISTS memory_guide_exposures (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    session_id        VARCHAR(36),
    trigger_slug      VARCHAR(255),
    candidate_slug    VARCHAR(255),
    strategy          VARCHAR(32),
    rank              INTEGER,
    shown_at          DATETIME,
    clicked_at        DATETIME,
    qualified_view_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_mastery_exposure_scope
    ON memory_guide_exposures (tenant_id, subject_id, shown_at);

CREATE TABLE IF NOT EXISTS memory_mastery_daily (
    id                VARCHAR(36) PRIMARY KEY,
    tenant_id         INTEGER NOT NULL,
    subject_id        VARCHAR(512) NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '',
    slug              VARCHAR(255) NOT NULL,
    event_type        VARCHAR(16) NOT NULL,
    event_date        TEXT NOT NULL,
    event_count       INTEGER NOT NULL DEFAULT 0,
    duration_sum      INTEGER NOT NULL DEFAULT 0,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_mastery_daily_scope
    ON memory_mastery_daily (tenant_id, subject_id, knowledge_base_id, slug, event_type, event_date);
