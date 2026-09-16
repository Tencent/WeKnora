-- Reverse 000095: drop experiment snapshot columns and index.

DROP INDEX IF EXISTS idx_evaluation_tasks_dataset_version;

ALTER TABLE evaluation_tasks
    DROP COLUMN IF EXISTS dataset_version_id,
    DROP COLUMN IF EXISTS dataset_content_sha256,
    DROP COLUMN IF EXISTS experiment_snapshot,
    DROP COLUMN IF EXISTS experiment_sha256;
