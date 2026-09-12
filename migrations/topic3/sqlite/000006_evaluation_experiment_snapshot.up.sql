-- Mirrors versioned migration 000095_evaluation_experiment_snapshot.

ALTER TABLE evaluation_tasks
    ADD COLUMN dataset_version_id TEXT;

ALTER TABLE evaluation_tasks
    ADD COLUMN dataset_content_sha256 TEXT CHECK (dataset_content_sha256 IS NULL OR length(dataset_content_sha256) = 64);

ALTER TABLE evaluation_tasks
    ADD COLUMN experiment_snapshot TEXT CHECK (experiment_snapshot IS NULL OR json_valid(experiment_snapshot));

ALTER TABLE evaluation_tasks
    ADD COLUMN experiment_sha256 TEXT CHECK (experiment_sha256 IS NULL OR length(experiment_sha256) = 64);

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_dataset_version
    ON evaluation_tasks (tenant_id, dataset_version_id)
    WHERE deleted_at IS NULL AND dataset_version_id IS NOT NULL;
