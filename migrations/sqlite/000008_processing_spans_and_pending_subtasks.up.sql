-- Mirrors versioned migrations:
--   000055_knowledge_processing_spans   knowledge_processing_spans table
--   000056_knowledge_pending_subtasks   knowledges.pending_subtasks_count
--   000066_expand_knowledge_span_name   name VARCHAR(255)

CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id    VARCHAR(64) NOT NULL,
    attempt         INTEGER NOT NULL DEFAULT 1,
    span_id         VARCHAR(64) NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(255) NOT NULL,
    kind            VARCHAR(16) NOT NULL,
    status          VARCHAR(16) NOT NULL,
    input           TEXT,
    output          TEXT,
    metadata        TEXT,
    error_code      VARCHAR(64),
    error_message   TEXT,
    error_detail    TEXT,
    started_at      DATETIME,
    finished_at     DATETIME,
    duration_ms     INTEGER,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_kpspan_attempt_span UNIQUE (knowledge_id, attempt, span_id)
);

CREATE INDEX IF NOT EXISTS idx_kpspan_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);

CREATE INDEX IF NOT EXISTS idx_kpspan_status_started
    ON knowledge_processing_spans (status, started_at);

CREATE INDEX IF NOT EXISTS idx_kpspan_parent
    ON knowledge_processing_spans (parent_span_id)
    WHERE parent_span_id IS NOT NULL;

ALTER TABLE knowledges ADD COLUMN pending_subtasks_count INTEGER NOT NULL DEFAULT 0;
