-- Persist the single owner attempt for each knowledge row and backfill it
-- from existing span/job ledgers.

ALTER TABLE knowledges
    ADD COLUMN current_process_attempt INTEGER NOT NULL DEFAULT 0;

CREATE TEMP TABLE tmp_knowledge_attempts AS
SELECT knowledge_id, MAX(max_attempt) AS max_attempt
FROM (
    SELECT knowledge_id, MAX(attempt) AS max_attempt
    FROM knowledge_processing_spans
    GROUP BY knowledge_id
    UNION ALL
    SELECT scope_id AS knowledge_id, MAX(process_attempt) AS max_attempt
    FROM task_jobs
    WHERE scope = 'knowledge'
    GROUP BY scope_id
)
GROUP BY knowledge_id;

UPDATE knowledges
SET current_process_attempt = (
    SELECT MAX(max_attempt)
    FROM tmp_knowledge_attempts
    WHERE tmp_knowledge_attempts.knowledge_id = knowledges.id
)
WHERE EXISTS (
    SELECT 1
    FROM tmp_knowledge_attempts
    WHERE tmp_knowledge_attempts.knowledge_id = knowledges.id
);

DROP TABLE tmp_knowledge_attempts;

-- Duplicate task_jobs are moved to a negative legacy namespace so the live
-- positive attempt key can be unique. Task Center list queries hide negative
-- attempts. This rewrite is intentionally not restored by the down migration.
WITH ranked AS (
    SELECT job_id,
           tenant_id,
           scope,
           scope_id,
           process_attempt,
           created_at,
           ROW_NUMBER() OVER (
               PARTITION BY tenant_id, scope, scope_id, process_attempt
               ORDER BY created_at DESC, job_id DESC
           ) AS attempt_rn,
           COALESCE(
               MIN(CASE WHEN process_attempt < 0 THEN process_attempt END)
                   OVER (PARTITION BY tenant_id, scope, scope_id),
               0
           ) AS min_existing_negative
    FROM task_jobs
),
duplicate_jobs AS (
    SELECT job_id,
           min_existing_negative,
           ROW_NUMBER() OVER (
               PARTITION BY tenant_id, scope, scope_id
               ORDER BY process_attempt, created_at DESC, job_id DESC
           ) AS global_rn
    FROM ranked
    WHERE attempt_rn > 1
)
UPDATE task_jobs
SET process_attempt = (
    SELECT min_existing_negative - global_rn
    FROM duplicate_jobs
    WHERE duplicate_jobs.job_id = task_jobs.job_id
)
WHERE job_id IN (SELECT job_id FROM duplicate_jobs);

CREATE UNIQUE INDEX IF NOT EXISTS uq_task_jobs_scope_attempt
    ON task_jobs(tenant_id, scope, scope_id, process_attempt);

CREATE INDEX IF NOT EXISTS idx_knowledges_current_attempt
    ON knowledges(tenant_id, id, current_process_attempt);
