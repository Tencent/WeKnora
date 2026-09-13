ALTER TABLE training_orchestration_jobs
    DROP COLUMN IF EXISTS changed_cluster_count;

ALTER TABLE training_orchestration_jobs
    DROP COLUMN IF EXISTS changed_evidence_count;

ALTER TABLE training_orchestration_jobs
    DROP COLUMN IF EXISTS changed_knowledge_count;

ALTER TABLE training_orchestration_jobs
    DROP COLUMN IF EXISTS changed_video_count;

ALTER TABLE training_orchestration_jobs
    DROP COLUMN IF EXISTS refresh_mode;
