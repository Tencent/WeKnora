-- Reverse 000019: drop per-question results and the tenant-scoped task key.

DROP TABLE IF EXISTS evaluation_question_results;

DROP INDEX IF EXISTS evaluation_tasks_tenant_id_id_uk;
