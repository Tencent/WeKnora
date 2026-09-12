-- Reverse 000096: drop per-question results and the tenant-scoped task key.

DROP TABLE IF EXISTS evaluation_question_results;

ALTER TABLE evaluation_tasks
    DROP CONSTRAINT IF EXISTS evaluation_tasks_tenant_id_id_uk;
