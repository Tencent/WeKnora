-- Migration: 000062_task_center_ledger (rollback)

DROP TABLE IF EXISTS task_executions;
DROP TABLE IF EXISTS task_jobs;
