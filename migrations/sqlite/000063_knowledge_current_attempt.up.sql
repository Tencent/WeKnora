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
           ROW_NUMBER() OVER (
               PARTITION BY tenant_id, scope, scope_id, process_attempt
               ORDER BY created_at DESC, job_id DESC
           ) AS rn
    FROM task_jobs
)
UPDATE task_jobs
SET process_attempt = -ABS(process_attempt) - (
    SELECT rn FROM ranked WHERE ranked.job_id = task_jobs.job_id
)
WHERE job_id IN (SELECT job_id FROM ranked WHERE rn > 1);

CREATE UNIQUE INDEX IF NOT EXISTS uq_task_jobs_scope_attempt
    ON task_jobs(tenant_id, scope, scope_id, process_attempt);

CREATE INDEX IF NOT EXISTS idx_knowledges_current_attempt
    ON knowledges(tenant_id, id, current_process_attempt);
