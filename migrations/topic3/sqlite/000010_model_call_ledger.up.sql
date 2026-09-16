CREATE TABLE IF NOT EXISTS model_price_versions (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    model_id TEXT NOT NULL,
    valid_from DATETIME NOT NULL,
    valid_to DATETIME,
    input_microunits_per_million INTEGER NOT NULL,
    output_microunits_per_million INTEGER NOT NULL,
    currency TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    CONSTRAINT model_price_versions_window_check CHECK (valid_to IS NULL OR valid_to > valid_from),
    CONSTRAINT model_price_versions_input_price_check CHECK (input_microunits_per_million >= 0),
    CONSTRAINT model_price_versions_output_price_check CHECK (output_microunits_per_million >= 0),
    CONSTRAINT model_price_versions_currency_check CHECK (length(currency) = 3),
    CONSTRAINT model_price_versions_identity_unique UNIQUE (tenant_id, model_id, valid_from)
);

CREATE INDEX IF NOT EXISTS idx_model_price_versions_effective
    ON model_price_versions (tenant_id, model_id, valid_from DESC);

CREATE TABLE IF NOT EXISTS model_call_records (
    id TEXT PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    evaluation_task_id TEXT NOT NULL DEFAULT '',
    model_id TEXT NOT NULL,
    model_snapshot TEXT NOT NULL,
    purpose TEXT NOT NULL,
    operation TEXT NOT NULL,
    started_at DATETIME NOT NULL,
    ended_at DATETIME,
    duration_ms INTEGER,
    status TEXT NOT NULL,
    error_code TEXT NOT NULL DEFAULT '',
    prompt_tokens INTEGER,
    completion_tokens INTEGER,
    total_tokens INTEGER,
    provider_cache_status TEXT NOT NULL DEFAULT 'unreported',
    provider_cache_read_tokens INTEGER,
    provider_cache_write_tokens INTEGER,
    provider_cache_miss_tokens INTEGER,
    application_cache_status TEXT NOT NULL DEFAULT 'unavailable',
    price_version_id TEXT,
    input_microunits_per_million INTEGER,
    output_microunits_per_million INTEGER,
    currency TEXT NOT NULL DEFAULT '',
    cost_microunits INTEGER,
    accounting_complete INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME,
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
