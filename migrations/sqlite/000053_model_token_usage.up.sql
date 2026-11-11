CREATE TABLE model_token_usages (
    id VARCHAR(36) PRIMARY KEY NOT NULL,
    tenant_id BIGINT NOT NULL,
    model_id TEXT NOT NULL,
    model_name TEXT NOT NULL,
    started_at DATETIME NOT NULL,
    usage_date VARCHAR(10) NOT NULL,
    usage_known BOOLEAN NOT NULL,
    completed BOOLEAN NOT NULL,
    input_tokens BIGINT NOT NULL,
    output_tokens BIGINT NOT NULL,
    total_tokens BIGINT NOT NULL
);

CREATE INDEX idx_model_token_usages_tenant_date ON model_token_usages (tenant_id, usage_date);
CREATE INDEX idx_model_token_usages_date ON model_token_usages (usage_date);
