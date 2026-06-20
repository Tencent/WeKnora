-- Create extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pg_search;


-- Create tenant table
CREATE TABLE IF NOT EXISTS tenants (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    api_key VARCHAR(256) NOT NULL,
    retriever_engines JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(50) DEFAULT 'active',
    business VARCHAR(255) NOT NULL,
    storage_quota BIGINT NOT NULL DEFAULT 10737418240, -- 默认10GB配额(Bytes)
    storage_used BIGINT NOT NULL DEFAULT 0, -- 已使用的存储空间(Bytes)
    agent_config JSONB DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

COMMENT ON COLUMN tenants.agent_config IS 'Tenant-level agent configuration in JSON format';

-- Set the starting value for tenants id sequence
ALTER SEQUENCE tenants_id_seq RESTART WITH 10000;

-- Add indexes
CREATE INDEX IF NOT EXISTS idx_tenants_api_key ON tenants(api_key);
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);

-- Create model table
CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    type VARCHAR(50) NOT NULL,
    source VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSONB NOT NULL,
    is_default BOOLEAN NOT NULL DEFAULT false,
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);  

-- Add indexes for models
CREATE INDEX IF NOT EXISTS idx_models_type ON models(type);
CREATE INDEX IF NOT EXISTS idx_models_source ON models(source);

-- Create knowledge_base table
CREATE TABLE IF NOT EXISTS knowledge_bases (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    tenant_id INTEGER NOT NULL,
    chunking_config JSONB NOT NULL DEFAULT '{"chunk_size": 512, "chunk_overlap": 50, "split_markers": ["\n\n", "\n", "。"], "keep_separator": true}',
    image_processing_config JSONB NOT NULL DEFAULT '{"enable_multimodal": false, "model_id": ""}',
    embedding_model_id VARCHAR(64) NOT NULL,
    summary_model_id VARCHAR(64) NOT NULL,
    rerank_model_id VARCHAR(64) NOT NULL,
    cos_config JSONB NOT NULL DEFAULT '{}',
    vlm_config JSONB NOT NULL DEFAULT '{}',
    extract_config JSONB NULL DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Add indexes for knowledge_bases
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_tenant_id ON knowledge_bases(tenant_id);

-- Create knowledge table
CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    source VARCHAR(2048) NOT NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
    current_process_attempt BIGINT NOT NULL DEFAULT 0,
    enable_status VARCHAR(50) NOT NULL DEFAULT 'enabled',
    embedding_model_id VARCHAR(64),
    file_name VARCHAR(255),
    file_type VARCHAR(50),
    file_size BIGINT,
    file_path TEXT,
    file_hash VARCHAR(64),
    storage_size BIGINT NOT NULL DEFAULT 0, -- 存储大小(Byte)
    metadata JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT,
    deleted_at TIMESTAMP WITH TIME ZONE
);

-- Add indexes for knowledge
CREATE INDEX IF NOT EXISTS idx_knowledges_tenant_id ON knowledges(tenant_id);
CREATE INDEX IF NOT EXISTS idx_knowledges_base_id ON knowledges(knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_knowledges_parse_status ON knowledges(parse_status);
CREATE INDEX IF NOT EXISTS idx_knowledges_enable_status ON knowledges(enable_status);

-- Create session table
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    title VARCHAR(255),
    description TEXT,
    knowledge_base_id VARCHAR(36),
    max_rounds INTEGER NOT NULL DEFAULT 5,
    enable_rewrite BOOLEAN NOT NULL DEFAULT true,
    fallback_strategy VARCHAR(255) NOT NULL DEFAULT 'fixed',
    fallback_response TEXT NOT NULL DEFAULT '很抱歉，我暂时无法回答这个问题。',
    keyword_threshold FLOAT NOT NULL DEFAULT 0.5,
    vector_threshold FLOAT NOT NULL DEFAULT 0.5,
    rerank_model_id VARCHAR(64),
    embedding_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_top_k INTEGER NOT NULL DEFAULT 10,
    rerank_threshold FLOAT NOT NULL DEFAULT 0.65,
    summary_model_id VARCHAR(64),
    summary_parameters JSONB NOT NULL DEFAULT '{}',
    agent_config JSONB DEFAULT NULL,
    context_config JSONB DEFAULT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

COMMENT ON COLUMN sessions.agent_config IS 'Session-level agent configuration in JSON format';
COMMENT ON COLUMN sessions.context_config IS 'LLM context management configuration (separate from message storage)';

-- Create Index for sessions
CREATE INDEX IF NOT EXISTS idx_sessions_tenant_id ON sessions(tenant_id);


-- Create message table
CREATE TABLE IF NOT EXISTS messages (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    request_id VARCHAR(36) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    role VARCHAR(50) NOT NULL,
    content TEXT NOT NULL,
    knowledge_references JSONB NOT NULL DEFAULT '[]',
    agent_steps JSONB DEFAULT NULL,
    is_completed BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

COMMENT ON COLUMN messages.agent_steps IS 'Agent execution steps (reasoning process and tool calls)';

-- Create Index for messages
CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id); 


CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    content TEXT NOT NULL,
    chunk_index INTEGER NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT true,
    start_at INTEGER NOT NULL,
    end_at INTEGER NOT NULL,
    pre_chunk_id VARCHAR(36),
    next_chunk_id VARCHAR(36),
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    parent_chunk_id VARCHAR(36),
    image_info TEXT,
    relation_chunks JSONB,
    indirect_relation_chunks JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_chunks_tenant_kg ON chunks(tenant_id, knowledge_id);
CREATE INDEX IF NOT EXISTS idx_chunks_parent_id ON chunks(parent_chunk_id);
CREATE INDEX IF NOT EXISTS idx_chunks_chunk_type ON chunks(chunk_type);

CREATE TABLE IF NOT EXISTS embeddings (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,

    source_id VARCHAR(64) NOT NULL,
    source_type INTEGER NOT NULL,
    chunk_id VARCHAR(64),
    knowledge_id VARCHAR(64),
    knowledge_base_id VARCHAR(64),
    content TEXT,
    dimension INTEGER NOT NULL,
    embedding halfvec
);

CREATE UNIQUE INDEX IF NOT EXISTS embeddings_unique_source ON embeddings(source_id, source_type);
CREATE INDEX IF NOT EXISTS embeddings_search_idx ON embeddings
USING bm25 (id, knowledge_base_id, content, knowledge_id, chunk_id)
WITH (
    key_field = 'id',
    text_fields = '{
        "content": {
          "tokenizer": {"type": "chinese_lindera"}
        }
    }'
);
CREATE INDEX ON embeddings USING hnsw ((embedding::halfvec(3584)) halfvec_cosine_ops) WITH (m = 16, ef_construction = 64) WHERE (dimension = 3584);
CREATE INDEX ON embeddings USING hnsw ((embedding::halfvec(798)) halfvec_cosine_ops) WITH (m = 16, ef_construction = 64) WHERE (dimension = 798);

CREATE TABLE IF NOT EXISTS task_jobs (
    job_id           VARCHAR(64) PRIMARY KEY,
    tenant_id        BIGINT NOT NULL,
    created_by       VARCHAR(64) NOT NULL DEFAULT '',
    kind             VARCHAR(32) NOT NULL,
    origin           VARCHAR(8) NOT NULL DEFAULT 'user',
    display_name     VARCHAR(255) NOT NULL DEFAULT '',
    scope            VARCHAR(32) NOT NULL,
    scope_id         VARCHAR(64) NOT NULL,
    related_id       VARCHAR(64) NOT NULL DEFAULT '',
    process_attempt  INT NOT NULL DEFAULT 0,
    state            VARCHAR(16) NOT NULL DEFAULT 'queued',
    metadata         JSONB NOT NULL DEFAULT '{}'::JSONB,
    replay_spec      JSONB NOT NULL DEFAULT '{}'::JSONB,
    last_error_class VARCHAR(24) NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    failed_task_type VARCHAR(64) NOT NULL DEFAULT '',
    failed_task_id   VARCHAR(64) NOT NULL DEFAULT '',
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at      TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_state_created
    ON task_jobs (tenant_id, state, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_kind_state
    ON task_jobs (tenant_id, kind, state);
CREATE INDEX IF NOT EXISTS idx_task_jobs_tenant_creator_created
    ON task_jobs (tenant_id, created_by, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_jobs_scope_attempt
    ON task_jobs (tenant_id, scope, scope_id, process_attempt);
CREATE INDEX IF NOT EXISTS idx_task_jobs_related_state
    ON task_jobs (tenant_id, related_id, state);

CREATE TABLE IF NOT EXISTS task_executions (
    execution_id    VARCHAR(64) PRIMARY KEY,
    job_id          VARCHAR(64) NOT NULL REFERENCES task_jobs(job_id) ON DELETE CASCADE,
    process_attempt INT NOT NULL DEFAULT 0,
    task_type       VARCHAR(64) NOT NULL,
    queue           VARCHAR(32) NOT NULL DEFAULT '',
    state           VARCHAR(16) NOT NULL DEFAULT 'queued',
    retry_count     INT NOT NULL DEFAULT 0,
    error_class     VARCHAR(24) NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    retry_of        VARCHAR(64) NOT NULL DEFAULT '',
    rescheduled_to_execution_id VARCHAR(64) NOT NULL DEFAULT '',
    enqueued_at     TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    dispatched_at   TIMESTAMP WITH TIME ZONE,
    started_at      TIMESTAMP WITH TIME ZONE,
    finished_at     TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_task_executions_job_attempt_enqueued
    ON task_executions (job_id, process_attempt, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_task_executions_job_state
    ON task_executions (job_id, state);
CREATE INDEX IF NOT EXISTS idx_task_executions_state_enqueued
    ON task_executions (state, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_task_executions_rescheduled_to
    ON task_executions (rescheduled_to_execution_id)
    WHERE rescheduled_to_execution_id <> '';
