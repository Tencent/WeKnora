-- Migration: 000029_web_search_providers
-- Description: Create web_search_providers table for tenant-specific search engine configurations
DO $$ BEGIN RAISE NOTICE '[Migration 000029] Creating web_search_providers table'; END $$;

-- Create web_search_providers table for managing tenant search engine configurations
-- Each row represents a configured search provider instance (e.g., "Production Bing", "Test Google")
-- Agents reference these by ID via custom_agents.config.web_search_provider_id
CREATE TABLE IF NOT EXISTS web_search_providers (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    provider VARCHAR(50) NOT NULL,
    description TEXT,
    parameters JSONB,
    is_default BOOLEAN DEFAULT false,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL
);

CREATE INDEX IF NOT EXISTS idx_web_search_providers_tenant_id ON web_search_providers (tenant_id);
CREATE INDEX IF NOT EXISTS idx_web_search_providers_provider ON web_search_providers (provider);
CREATE INDEX IF NOT EXISTS idx_web_search_providers_deleted_at ON web_search_providers (deleted_at);

-- Auto-update updated_at column (reuses the function from migration 000028)
CREATE TRIGGER trg_web_search_providers_updated_at
    BEFORE UPDATE ON web_search_providers
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

DO $$ BEGIN RAISE NOTICE '[Migration 000029] web_search_providers table created successfully'; END $$;
