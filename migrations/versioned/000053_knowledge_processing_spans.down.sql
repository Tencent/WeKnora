-- Migration: 000053_knowledge_processing_spans (rollback)
-- Restores the 000052 flat-stage table so the system is operational on a
-- back-rev. We do NOT migrate data back — the spans table carries strict
-- supersets of the stages table's information, so a forward-and-back round
-- trip silently drops the new tree shape (acceptable; rollback is a rare
-- emergency lever).
DO $$ BEGIN RAISE NOTICE '[Migration 000053 rollback] Dropping spans, restoring stages table'; END $$;

DROP INDEX IF EXISTS idx_kpspan_parent;
DROP INDEX IF EXISTS idx_kpspan_status_started;
DROP INDEX IF EXISTS idx_kpspan_knowledge_attempt;
DROP TABLE IF EXISTS knowledge_processing_spans;

CREATE TABLE IF NOT EXISTS knowledge_processing_stages (
    id              BIGSERIAL PRIMARY KEY,
    knowledge_id    VARCHAR(64)              NOT NULL,
    stage           VARCHAR(32)              NOT NULL,
    status          VARCHAR(16)              NOT NULL,
    started_at      TIMESTAMP WITH TIME ZONE,
    finished_at     TIMESTAMP WITH TIME ZONE,
    duration_ms     BIGINT,
    error_code      VARCHAR(64),
    error_message   TEXT,
    error_detail    TEXT,
    attempt         INT                      NOT NULL DEFAULT 1,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_kps_knowledge_stage UNIQUE (knowledge_id, stage)
);
CREATE INDEX IF NOT EXISTS idx_kps_knowledge ON knowledge_processing_stages (knowledge_id);
CREATE INDEX IF NOT EXISTS idx_kps_status_started ON knowledge_processing_stages (status, started_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000053 rollback] complete'; END $$;
