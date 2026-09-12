-- Evaluation experiment snapshot: dataset version binding and immutable experiment manifest.
-- Reserved parallel-draft migration number for M3 slice 2 (see docs/evaluation-m2-m5-target-architecture.md section 10).
-- All four columns stay nullable so pre-M3 tasks keep null provenance instead of
-- being backfilled with fabricated values.

ALTER TABLE evaluation_tasks
    ADD COLUMN IF NOT EXISTS dataset_version_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS dataset_content_sha256 CHAR(64),
    ADD COLUMN IF NOT EXISTS experiment_snapshot JSONB,
    ADD COLUMN IF NOT EXISTS experiment_sha256 CHAR(64);

COMMENT ON COLUMN evaluation_tasks.dataset_version_id IS
    'Globally unique evaluation_dataset_versions.id frozen at task creation; null for pre-M3 tasks.';
COMMENT ON COLUMN evaluation_tasks.dataset_content_sha256 IS
    'Canonical content SHA-256 of the bound dataset version; null for pre-M3 tasks.';
COMMENT ON COLUMN evaluation_tasks.experiment_snapshot IS
    'Schema-version-1 immutable experiment manifest (dataset, models, resolved configuration, metric plan, code, environment).';
COMMENT ON COLUMN evaluation_tasks.experiment_sha256 IS
    'Canonical SHA-256 of experiment_snapshot for tamper evidence and comparison.';

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_dataset_version
    ON evaluation_tasks (tenant_id, dataset_version_id)
    WHERE deleted_at IS NULL AND dataset_version_id IS NOT NULL;
