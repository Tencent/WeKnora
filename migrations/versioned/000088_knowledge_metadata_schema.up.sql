-- Knowledge-base custom metadata schema (typed definitions and document values).

CREATE TABLE IF NOT EXISTS knowledge_metadata_definitions (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    name VARCHAR(128) NOT NULL,
    normalized_name VARCHAR(128) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    value_type VARCHAR(32) NOT NULL CHECK (value_type IN (
        'text', 'single_select', 'multi_select', 'number', 'date', 'boolean'
    )),
    required BOOLEAN NOT NULL DEFAULT FALSE,
    filterable BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, knowledge_base_id, normalized_name)
);

CREATE INDEX IF NOT EXISTS idx_metadata_definitions_kb_status_sort
    ON knowledge_metadata_definitions (tenant_id, knowledge_base_id, status, sort_order);

CREATE TABLE IF NOT EXISTS knowledge_metadata_options (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    metadata_definition_id VARCHAR(36) NOT NULL
        REFERENCES knowledge_metadata_definitions(id) ON DELETE CASCADE,
    label VARCHAR(128) NOT NULL,
    normalized_label VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (metadata_definition_id, normalized_label)
);

CREATE INDEX IF NOT EXISTS idx_metadata_options_definition
    ON knowledge_metadata_options (metadata_definition_id, status, sort_order);

CREATE TABLE IF NOT EXISTS knowledge_metadata_values (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    knowledge_id VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    metadata_definition_id VARCHAR(36) NOT NULL
        REFERENCES knowledge_metadata_definitions(id) ON DELETE CASCADE,
    text_value TEXT,
    number_value DOUBLE PRECISION,
    date_value DATE,
    bool_value BOOLEAN,
    source VARCHAR(16) NOT NULL CHECK (source IN ('automatic', 'manual')),
    review_status VARCHAR(16) NOT NULL CHECK (review_status IN ('pending', 'confirmed')),
    allow_auto_overwrite BOOLEAN NOT NULL DEFAULT FALSE,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    auto_rule_id VARCHAR(36),
    auto_rule_revision INTEGER,
    updated_by VARCHAR(36),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (knowledge_id, metadata_definition_id),
    CHECK (num_nonnulls(text_value, number_value, date_value, bool_value) <= 1),
    CHECK (source <> 'manual' OR review_status = 'confirmed')
);

CREATE INDEX IF NOT EXISTS idx_metadata_values_scope
    ON knowledge_metadata_values (tenant_id, knowledge_base_id, knowledge_id);
CREATE INDEX IF NOT EXISTS idx_metadata_values_definition
    ON knowledge_metadata_values (metadata_definition_id);

CREATE TABLE IF NOT EXISTS knowledge_metadata_value_options (
    metadata_value_id VARCHAR(36) NOT NULL
        REFERENCES knowledge_metadata_values(id) ON DELETE CASCADE,
    option_id VARCHAR(36) NOT NULL
        REFERENCES knowledge_metadata_options(id) ON DELETE CASCADE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (metadata_value_id, option_id)
);

CREATE INDEX IF NOT EXISTS idx_metadata_value_options_option
    ON knowledge_metadata_value_options (option_id, metadata_value_id);

CREATE TABLE IF NOT EXISTS knowledge_metadata_auto_rules (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    metadata_definition_id VARCHAR(36) NOT NULL
        REFERENCES knowledge_metadata_definitions(id) ON DELETE CASCADE,
    strategy VARCHAR(32) NOT NULL CHECK (strategy IN ('source_mapping', 'llm_extract')),
    config JSONB NOT NULL DEFAULT '{}',
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_metadata_auto_rules_scope
    ON knowledge_metadata_auto_rules (tenant_id, knowledge_base_id, metadata_definition_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_metadata_auto_rules_enabled_definition
    ON knowledge_metadata_auto_rules (metadata_definition_id) WHERE enabled = TRUE;
