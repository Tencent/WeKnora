-- Migration: 000029_datasource_tables
-- Description: Create data_sources and sync_logs tables for external data source management
DO $$ BEGIN RAISE NOTICE '[Migration 000029] Creating data_sources and sync_logs tables'; END $$;

-- Create data_sources table for managing external data source configurations
CREATE TABLE IF NOT EXISTS data_sources (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(255) NOT NULL,
    type VARCHAR(50) NOT NULL,
    config JSONB,
    sync_schedule VARCHAR(100),
    sync_mode VARCHAR(20) DEFAULT 'incremental',
    status VARCHAR(32) DEFAULT 'active',
    conflict_strategy VARCHAR(32) DEFAULT 'overwrite',
    sync_deletions BOOLEAN DEFAULT true,
    last_sync_at TIMESTAMP NULL,
    last_sync_cursor JSONB,
    last_sync_result JSONB,
    error_message TEXT,
    sync_log_retention_days INT DEFAULT 30,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);

CREATE INDEX IF NOT EXISTS idx_data_sources_tenant_id ON data_sources (tenant_id);
CREATE INDEX IF NOT EXISTS idx_data_sources_knowledge_base_id ON data_sources (knowledge_base_id);
CREATE INDEX IF NOT EXISTS idx_data_sources_type ON data_sources (type);
CREATE INDEX IF NOT EXISTS idx_data_sources_status ON data_sources (status);
CREATE INDEX IF NOT EXISTS idx_data_sources_deleted_at ON data_sources (deleted_at);

-- Create sync_logs table for tracking sync operation history
CREATE TABLE IF NOT EXISTS sync_logs (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    data_source_id VARCHAR(36) NOT NULL REFERENCES data_sources(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    status VARCHAR(32) NOT NULL,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    finished_at TIMESTAMP NULL,
    items_total INT DEFAULT 0,
    items_created INT DEFAULT 0,
    items_updated INT DEFAULT 0,
    items_deleted INT DEFAULT 0,
    items_skipped INT DEFAULT 0,
    items_failed INT DEFAULT 0,
    error_message TEXT,
    result JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_sync_logs_data_source_id ON sync_logs (data_source_id);
CREATE INDEX IF NOT EXISTS idx_sync_logs_tenant_id ON sync_logs (tenant_id);
CREATE INDEX IF NOT EXISTS idx_sync_logs_status ON sync_logs (status);
CREATE INDEX IF NOT EXISTS idx_sync_logs_started_at ON sync_logs (started_at);


DO $$ BEGIN RAISE NOTICE '[Migration 000029] data_sources and sync_logs tables created successfully'; END $$;
