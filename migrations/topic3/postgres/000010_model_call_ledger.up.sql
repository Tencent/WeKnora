CREATE TABLE IF NOT EXISTS model_price_versions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL,
    valid_to TIMESTAMPTZ,
    input_microunits_per_million BIGINT NOT NULL,
    output_microunits_per_million BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT model_price_versions_window_check CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT model_price_versions_input_price_check CHECK (input_microunits_per_million >= 0),
    CONSTRAINT model_price_versions_output_price_check CHECK (output_microunits_per_million >= 0),
    CONSTRAINT model_price_versions_currency_check CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT model_price_versions_identity_unique UNIQUE (tenant_id, model_id, valid_from)
);

CREATE INDEX IF NOT EXISTS idx_model_price_versions_effective
    ON model_price_versions (tenant_id, model_id, valid_from DESC);

CREATE TABLE IF NOT EXISTS model_call_records (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    evaluation_task_id VARCHAR(128) NOT NULL DEFAULT '',
    model_id VARCHAR(64) NOT NULL,
    model_snapshot JSONB NOT NULL,
    purpose VARCHAR(64) NOT NULL,
    operation VARCHAR(32) NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ,
    duration_ms BIGINT,
    status VARCHAR(16) NOT NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens BIGINT,
    completion_tokens BIGINT,
    total_tokens BIGINT,
    provider_cache_status VARCHAR(16) NOT NULL DEFAULT 'unreported',
    provider_cache_read_tokens BIGINT,
    provider_cache_write_tokens BIGINT,
    provider_cache_miss_tokens BIGINT,
    application_cache_status VARCHAR(16) NOT NULL DEFAULT 'unavailable',
    price_version_id VARCHAR(36),
    input_microunits_per_million BIGINT,
    output_microunits_per_million BIGINT,
    currency CHAR(3) NOT NULL DEFAULT '',
    cost_microunits BIGINT,
    accounting_complete BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT model_call_records_status_check CHECK (status IN ('started', 'success', 'error', 'canceled')),
    CONSTRAINT model_call_records_duration_check CHECK (duration_ms IS NULL OR duration_ms >= 0),
    CONSTRAINT model_call_records_token_check CHECK (
        (prompt_tokens IS NULL OR prompt_tokens >= 0) AND
        (completion_tokens IS NULL OR completion_tokens >= 0) AND
        (total_tokens IS NULL OR total_tokens >= 0)
    ),
    CONSTRAINT model_call_records_cost_check CHECK (cost_microunits IS NULL OR cost_microunits >= 0),
    CONSTRAINT model_call_records_price_fk FOREIGN KEY (price_version_id) REFERENCES model_price_versions (id)
);

CREATE INDEX IF NOT EXISTS idx_model_call_records_tenant_started
    ON model_call_records (tenant_id, started_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_model_call_records_tenant_model_started
    ON model_call_records (tenant_id, model_id, started_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_model_call_records_evaluation_task
    ON model_call_records (tenant_id, evaluation_task_id, started_at, id)
    WHERE deleted_at IS NULL AND evaluation_task_id <> '';
