-- Migration: 000052_knowledge_processing_stages
-- Per-knowledge stage progress for the document parsing pipeline.
--
-- Background: knowledge.parse_status is a 4-state field (pending /
-- processing / completed / failed). When users hit "stuck in processing",
-- they have no way to tell which stage is actually running — DocReader,
-- chunking, embedding, multimodal OCR/VLM, or the final post-process
-- handoff. Operators face the same problem: a 500MB PDF stuck in
-- "processing" for 90 minutes could be legitimate OCR work or a frozen
-- docreader call, and the only signal is the absence of progress.
--
-- This table persists per-stage progress so:
--   1. The frontend can render a five-segment timeline showing where
--      each document is in the pipeline (PR③ work).
--   2. Failures carry a stable error_code (DOCREADER_TIMEOUT,
--      EMBEDDING_RATE_LIMIT, ...) the UI can map to localized
--      remediation text.
--   3. Operators can SQL-query "all rows stuck on stage=embedding for
--      >30 min" without log-scraping.
--
-- Schema decisions:
--   - One row per (knowledge_id, stage). Retry attempts BUMP attempt
--     counter and reset status; we don't keep historical attempt rows
--     because the timeline UI only needs the latest state per stage.
--   - error_message holds the user-facing summary (≤1KB);
--     error_detail is the full stack/payload (≤8KB) gated behind
--     admin views in the UI.
--   - Sweeps that recover stuck knowledge will leave these rows
--     intact so operators can post-mortem the failure.
DO $$ BEGIN RAISE NOTICE '[Migration 000052] Creating table: knowledge_processing_stages'; END $$;

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

-- Primary read path: "give me every stage row for this knowledge_id".
-- The unique constraint already covers point lookups by (knowledge_id,
-- stage); this index lets us fetch all 5 rows for a knowledge with one
-- index range scan, which is the API surface the frontend timeline calls.
CREATE INDEX IF NOT EXISTS idx_kps_knowledge
    ON knowledge_processing_stages (knowledge_id);

-- Operator query: "find stages stuck in running too long". Used by the
-- housekeeping cron in a future iteration and by ad-hoc SQL diagnostics.
CREATE INDEX IF NOT EXISTS idx_kps_status_started
    ON knowledge_processing_stages (status, started_at);

DO $$ BEGIN RAISE NOTICE '[Migration 000052] knowledge_processing_stages table ready'; END $$;
