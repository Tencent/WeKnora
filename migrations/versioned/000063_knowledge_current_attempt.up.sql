-- Migration: 000063_knowledge_current_attempt
-- Description: Persist the single owner attempt for each knowledge row and
--              backfill it from existing span/job ledgers.

DO $$ BEGIN RAISE NOTICE '[Migration 000063] Adding knowledges.current_process_attempt'; END $$;

ALTER TABLE knowledges
    ADD COLUMN IF NOT EXISTS current_process_attempt BIGINT NOT NULL DEFAULT 0;

WITH span_attempts AS (
    SELECT knowledge_id, MAX(attempt)::BIGINT AS max_attempt
    FROM knowledge_processing_spans
    GROUP BY knowledge_id
),
job_attempts AS (
    SELECT scope_id AS knowledge_id, MAX(process_attempt)::BIGINT AS max_attempt
    FROM task_jobs
    WHERE scope = 'knowledge'
    GROUP BY scope_id
),
attempts AS (
    SELECT knowledge_id, MAX(max_attempt) AS max_attempt
    FROM (
        SELECT knowledge_id, max_attempt FROM span_attempts
        UNION ALL
        SELECT knowledge_id, max_attempt FROM job_attempts
    ) x
    GROUP BY knowledge_id
)
UPDATE knowledges k
SET current_process_attempt = GREATEST(k.current_process_attempt, COALESCE(a.max_attempt, 0))
FROM attempts a
WHERE k.id = a.knowledge_id;

-- Existing duplicates are not discarded: they are moved to a negative
-- process_attempt namespace so the live positive attempt key can be enforced
-- without deleting audit history. Application code only creates positive
-- attempts through BeginKnowledgeAttempt, and Task Center list queries hide
-- negative attempts as legacy duplicates. This rewrite is intentionally not
-- reversible by the down migration; restoring exact duplicates would break
-- the uniqueness invariant this migration introduces.
WITH ranked AS (
    SELECT job_id,
           ROW_NUMBER() OVER (
               PARTITION BY tenant_id, scope, scope_id, process_attempt
               ORDER BY created_at DESC, job_id DESC
           ) AS rn
    FROM task_jobs
)
UPDATE task_jobs j
SET process_attempt = -ABS(j.process_attempt) - ranked.rn
FROM ranked
WHERE j.job_id = ranked.job_id
  AND ranked.rn > 1;

CREATE UNIQUE INDEX IF NOT EXISTS uq_task_jobs_scope_attempt
    ON task_jobs (tenant_id, scope, scope_id, process_attempt);

CREATE INDEX IF NOT EXISTS idx_knowledges_current_attempt
    ON knowledges (tenant_id, id, current_process_attempt);

DO $$ BEGIN RAISE NOTICE '[Migration 000063] knowledge current attempt applied successfully'; END $$;
