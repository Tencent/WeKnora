ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS refresh_mode VARCHAR(32);

ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS changed_video_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS changed_knowledge_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS changed_evidence_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE training_orchestration_jobs
    ADD COLUMN IF NOT EXISTS changed_cluster_count INTEGER NOT NULL DEFAULT 0;
