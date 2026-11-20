DROP TABLE IF EXISTS wiki_ingest_page_progress;
DROP TABLE IF EXISTS wiki_ingest_plans;
ALTER TABLE task_pending_ops DROP COLUMN claim_epoch;
