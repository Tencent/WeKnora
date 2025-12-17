-- Migration: 000000_init
-- Description: Initialize database schema
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Starting initial database setup...'; END $$;

-- Create extensions
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating extensions...'; END $$;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create tenant table
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: tenants'; END $$;
CREATE TABLE IF NOT EXISTS tenants (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    api_key VARCHAR(64) NOT NULL,
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

-- Set the starting value for tenants id sequence (only if current value is less than 10000)
DO $$
DECLARE
    current_val BIGINT;
BEGIN
    SELECT last_value INTO current_val FROM tenants_id_seq;
    IF current_val < 10000 THEN
        ALTER SEQUENCE tenants_id_seq RESTART WITH 10000;
        RAISE NOTICE '[Migration 000000] Set tenants_id_seq to start at 10000';
    ELSE
        RAISE NOTICE '[Migration 000000] tenants_id_seq already at % (>= 10000), skipping', current_val;
    END IF;
EXCEPTION
    WHEN undefined_table THEN
        -- Sequence doesn't exist yet, will be created with table
        RAISE NOTICE '[Migration 000000] tenants_id_seq not found, will be created with table';
END $$;

-- Add indexes
CREATE INDEX IF NOT EXISTS idx_tenants_api_key ON tenants(api_key);
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);

-- Create model table
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: models'; END $$;
CREATE TABLE IF NOT EXISTS models (
    id VARCHAR(64) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
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
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: knowledge_bases'; END $$;
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
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: knowledges'; END $$;
CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    source VARCHAR(128) NOT NULL,
    parse_status VARCHAR(50) NOT NULL DEFAULT 'unprocessed',
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
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: sessions'; END $$;
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

-- Create Index for sessions
CREATE INDEX IF NOT EXISTS idx_sessions_tenant_id ON sessions(tenant_id);


-- Create message table
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: messages'; END $$;
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

-- Create Index for messages
CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id); 

-- Create chunks table
DO $$ BEGIN RAISE NOTICE '[Migration 000000] Creating table: chunks'; END $$;
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

DO $$ BEGIN RAISE NOTICE '[Migration 000000] Initial database setup completed successfully!'; END $$;