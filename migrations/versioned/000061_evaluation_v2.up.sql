CREATE TABLE evaluation_datasets (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(), tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL, description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX idx_evaluation_datasets_tenant ON evaluation_datasets (tenant_id, created_at DESC);

CREATE TABLE evaluation_samples (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(), tenant_id BIGINT NOT NULL,
    dataset_id VARCHAR(36) NOT NULL REFERENCES evaluation_datasets(id), question TEXT NOT NULL,
    reference_answer TEXT NOT NULL, reference_contexts JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMPTZ
);
CREATE INDEX idx_evaluation_samples_dataset ON evaluation_samples (tenant_id, dataset_id, created_at);

CREATE TABLE evaluation_runs (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(), tenant_id BIGINT NOT NULL,
    dataset_id VARCHAR(36) NOT NULL, dataset_name VARCHAR(255) NOT NULL, status VARCHAR(32) NOT NULL,
    config_snapshot JSONB NOT NULL, aggregate_metric_scores JSONB NOT NULL DEFAULT '{}',
    total_samples INTEGER NOT NULL DEFAULT 0, finished_samples INTEGER NOT NULL DEFAULT 0,
    failed_samples INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_evaluation_runs_tenant ON evaluation_runs (tenant_id, created_at DESC);
CREATE INDEX idx_evaluation_runs_dataset ON evaluation_runs (tenant_id, dataset_id);

CREATE TABLE evaluation_run_results (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(), tenant_id BIGINT NOT NULL,
    run_id VARCHAR(36) NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    sample_id VARCHAR(36) NOT NULL, sample_index INTEGER NOT NULL, question TEXT NOT NULL,
    reference_answer TEXT NOT NULL, reference_contexts JSONB NOT NULL DEFAULT '[]',
    retrieved_contexts JSONB NOT NULL DEFAULT '[]', generated_answer TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL, error TEXT NOT NULL DEFAULT '', metric_scores JSONB NOT NULL DEFAULT '{}',
    duration_milliseconds BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (run_id, sample_id)
);
CREATE INDEX idx_evaluation_run_results_run ON evaluation_run_results (tenant_id, run_id, sample_index);
