-- Migration: 000052_knowledge_processing_spans
-- Per-(knowledge, attempt) span tree for the document parsing pipeline,
-- inspired by Langfuse's trace / span / generation hierarchy.
--
-- Background: knowledge.parse_status is a 4-state field (pending /
-- processing / completed / failed). When users hit "stuck in processing",
-- they have no way to tell which stage is actually running — DocReader,
-- chunking, embedding, multimodal OCR/VLM, or the final post-process
-- handoff. Operators face the same problem.
--
-- This table persists per-stage progress (and finer-grained subspans) so:
--   1. The frontend can render a five-segment timeline showing where
--      each document is in the pipeline, with collapsible per-image /
--      per-batch subspans underneath.
--   2. Failures carry a stable error_code (DOCREADER_TIMEOUT,
--      EMBEDDING_RATE_LIMIT, ...) the UI can map to localized
--      remediation text.
--   3. Reparse history is preserved across attempts — operators can
--      navigate ?attempt=N to post-mortem "why did it fail twice?".
--   4. Cascade-cancel rules use the parent_span_id tree to flip
--      dependent subspans/stages to "cancelled" when an upstream
--      span fails, so the UI shows a clear blast radius instead of
--      orphan spinners.
--
-- Schema mirrors Langfuse's vocabulary:
--   * one ROOT span per (knowledge_id, attempt) acting as the trace
--   * STAGE spans (docreader/chunking/embedding/multimodal/postprocess)
--     are children of root
--   * SUBSPANs (multimodal.image[i], embedding.batch[i], postprocess.spawn.X)
--     hang off their stage. The kind="generation" subset corresponds 1:1 to
--     a Langfuse generation; metadata.langfuse_trace_id stitches them.
DO $$ BEGIN RAISE NOTICE '[Migration 000052] Creating table: knowledge_processing_spans'; END $$;

CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              BIGSERIAL                PRIMARY KEY,
    knowledge_id    VARCHAR(64)              NOT NULL,
    attempt         INT                      NOT NULL DEFAULT 1,
    span_id         VARCHAR(64)              NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(64)              NOT NULL,
    kind            VARCHAR(16)              NOT NULL,                       -- root / stage / subspan / generation
    status          VARCHAR(16)              NOT NULL,                       -- pending/running/done/failed/skipped/cancelled
    input           JSONB,
    output          JSONB,
    metadata        JSONB,
    error_code      VARCHAR(64),
    error_message   TEXT,
    error_detail    TEXT,
    started_at      TIMESTAMP WITH TIME ZONE,
    finished_at     TIMESTAMP WITH TIME ZONE,
    duration_ms     BIGINT,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_kpspan_attempt_span UNIQUE (knowledge_id, attempt, span_id)
);

-- Primary read path: fetch every span for a (knowledge, attempt) tuple in
-- one indexed range scan, then build the tree in memory. The unique
-- constraint above already covers point lookups.
CREATE INDEX IF NOT EXISTS idx_kpspan_knowledge_attempt
    ON knowledge_processing_spans (knowledge_id, attempt);

-- Operator query: "find spans stuck in running too long". Used by ad-hoc
-- diagnostics and the housekeeping sweep.
CREATE INDEX IF NOT EXISTS idx_kpspan_status_started
    ON knowledge_processing_spans (status, started_at);

-- Lineage walks: cascade-cancel a stage's downstream needs to find every
-- child by parent_span_id. The cardinality is small (≤ tens per attempt)
-- so we don't need a covering index, just B-tree on parent.
CREATE INDEX IF NOT EXISTS idx_kpspan_parent
    ON knowledge_processing_spans (parent_span_id)
    WHERE parent_span_id IS NOT NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000052] knowledge_processing_spans table ready'; END $$;
