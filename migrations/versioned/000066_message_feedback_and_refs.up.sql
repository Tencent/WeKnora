-- Migration: 000066_message_feedback_and_refs
-- Description:
--   1. message_feedback – records a user's like/dislike/cancel action on a
--      specific assistant message, plus an optional dislike reason. One row
--      per (user, message); toggling replaces the existing row.
--   2. message_chunk_refs – normalized many-to-many link between an assistant
--      message and the knowledge chunks it cited. Populated from the JSON
--      knowledge_references column so stats queries (session count per chunk,
--      reason aggregation) can use plain SQL JOINs instead of JSON parsing.

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Creating table: message_feedback'; END $$;

CREATE TABLE IF NOT EXISTS message_feedback (
    id             VARCHAR(36)   NOT NULL,
    tenant_id      BIGINT        NOT NULL,
    user_id        VARCHAR(512)  NOT NULL,
    session_id     VARCHAR(36)   NOT NULL,
    message_id     VARCHAR(36)   NOT NULL,
    feedback_type  VARCHAR(16)   NOT NULL,   -- 'like' | 'dislike' | 'none'
    reason         VARCHAR(64),               -- dislike reason code (e.g. 'irrelevant')
    reason_detail  TEXT,                      -- free-text supplementary note
    created_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
);

-- One active feedback per (user, message). upsert-friendly.
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_feedback_user_message
    ON message_feedback (user_id, message_id);

-- "list feedback for a chunk" requires JOINing through refs, but we also
-- want direct per-session / per-message lookups.
CREATE INDEX IF NOT EXISTS idx_message_feedback_tenant_session
    ON message_feedback (tenant_id, session_id);
CREATE INDEX IF NOT EXISTS idx_message_feedback_message
    ON message_feedback (message_id);
CREATE INDEX IF NOT EXISTS idx_message_feedback_tenant_type
    ON message_feedback (tenant_id, feedback_type);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] Creating table: message_chunk_refs'; END $$;

CREATE TABLE IF NOT EXISTS message_chunk_refs (
    id           VARCHAR(36)  NOT NULL,
    tenant_id    BIGINT       NOT NULL,
    message_id   VARCHAR(36)  NOT NULL,
    session_id   VARCHAR(36)  NOT NULL,
    chunk_id     VARCHAR(36)  NOT NULL,
    knowledge_id VARCHAR(36)  NOT NULL DEFAULT '',
    kb_id        VARCHAR(36)  NOT NULL DEFAULT '',
    created_at   TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id)
);

-- One ref per (message, chunk) to avoid duplicates on re-population.
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_chunk_refs_message_chunk
    ON message_chunk_refs (message_id, chunk_id);

-- Stats: "how many distinct sessions cited this chunk?"
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_tenant_chunk
    ON message_chunk_refs (tenant_id, chunk_id);
-- Cleanup path when a session is deleted.
CREATE INDEX IF NOT EXISTS idx_message_chunk_refs_session
    ON message_chunk_refs (session_id);

DO $$ BEGIN RAISE NOTICE '[Migration 000066] message_feedback and message_chunk_refs ready'; END $$;
