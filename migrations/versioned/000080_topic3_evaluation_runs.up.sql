CREATE TABLE IF NOT EXISTS evaluation_runs (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    dataset_id VARCHAR(255) NOT NULL,
    status SMALLINT NOT NULL,
    detail JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_evaluation_runs_tenant_created
    ON evaluation_runs (tenant_id, created_at DESC);
