CREATE TABLE IF NOT EXISTS training_orchestration_jobs (
    id VARCHAR(36) PRIMARY KEY,
    owner_scope_id VARCHAR(128) NOT NULL,
    status VARCHAR(24) NOT NULL,
    progress INTEGER NOT NULL DEFAULT 0,
    source_fingerprint VARCHAR(80),
    input_references TEXT,
    result_wiki_page_id VARCHAR(64),
    model VARCHAR(128),
    prompt_version VARCHAR(64),
    error_code VARCHAR(64),
    error_message TEXT,
    reused BOOLEAN NOT NULL DEFAULT FALSE,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_training_jobs_owner_created ON training_orchestration_jobs(owner_scope_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_training_jobs_status ON training_orchestration_jobs(status);
CREATE INDEX IF NOT EXISTS idx_training_jobs_fingerprint ON training_orchestration_jobs(source_fingerprint);

CREATE TABLE IF NOT EXISTS training_orchestration_currents (
    owner_scope_id VARCHAR(128) PRIMARY KEY,
    job_id VARCHAR(36) NOT NULL,
    result_wiki_page_id VARCHAR(64) NOT NULL,
    source_fingerprint VARCHAR(80) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_training_current_job ON training_orchestration_currents(job_id);
CREATE INDEX IF NOT EXISTS idx_training_current_wiki ON training_orchestration_currents(result_wiki_page_id);
