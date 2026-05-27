-- Migration: 000053_knowledge_processing_spans
-- Replace the flat 5-stage tracking from migration 000052 with a tree-shaped
-- span model inspired by Langfuse traces.
--
-- Why redesign?
--
-- The flat (knowledge_id, stage) model from 000052 had four shortcomings the
-- "stuck parsing" UX requires us to solve:
--
--   1. Reparse semantics — a re-trigger of parsing on the same knowledge
--      reset the existing rows, erasing the previous attempt's history.
--      Operators investigating "why did it fail twice?" had no record of
--      attempt 1 once attempt 2 began.
--
--   2. No DAG — Embedding and Multimodal both depend on Chunking but are
--      independent of each other. The flat model couldn't represent that,
--      so a Chunking failure couldn't auto-cascade "cancelled" to its
--      downstream stages and the UI had to guess.
--
--   3. No subspans — a Multimodal stage produces N parallel image tasks;
--      Embedding produces M batches; PostProcess fans out into Summary /
--      Question / Wiki / Graph. Those finer-grained units have their own
--      success / failure / duration that operators want to see (and that
--      Langfuse generations already capture for the LLM-call subset).
--
--   4. No input/output — duration alone hides the WHY of a slow run. With
--      input ({pages:48, images:5}) and output ({tokens:5840, dim:1024})
--      JSON, the timeline answers "how big was the work" without a log
--      dive.
--
-- The new schema mirrors Langfuse's trace/span/generation hierarchy:
--   * one ROOT span per (knowledge_id, attempt) acting as the trace
--   * STAGE spans (docreader/chunking/embedding/multimodal/postprocess)
--     are children of root
--   * SUBSPANs (multimodal.image[i], embedding.batch[i], postprocess.spawn.X)
--     hang off their stage. The kind="generation" subset corresponds 1:1 to
--     a Langfuse generation; metadata.langfuse_trace_id stitches them.
--
-- We keep the OLD 000052 table untouched on rollback so a `migrate down`
-- doesn't lose either schema. The forward path drops it because no UI
-- callers use it (the API was only added in the same branch and is being
-- replaced as part of this migration).
DO $$ BEGIN RAISE NOTICE '[Migration 000053] Replacing knowledge_processing_stages with knowledge_processing_spans'; END $$;

DROP INDEX IF EXISTS idx_kps_status_started;
DROP INDEX IF EXISTS idx_kps_knowledge;
DROP TABLE IF EXISTS knowledge_processing_stages;

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
-- diagnostics and a future housekeeping sweep.
CREATE INDEX IF NOT EXISTS idx_kpspan_status_started
    ON knowledge_processing_spans (status, started_at);

-- Lineage walks: cascade-cancel a stage's downstream needs to find every
-- child by parent_span_id. The cardinality is small (≤ tens per attempt)
-- so we don't need a covering index, just B-tree on parent.
CREATE INDEX IF NOT EXISTS idx_kpspan_parent
    ON knowledge_processing_spans (parent_span_id)
    WHERE parent_span_id IS NOT NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000053] knowledge_processing_spans table ready'; END $$;
