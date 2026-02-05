-- Migration: 000012_organizations (down, merged 000012, 000013, 000014)
DO $$ BEGIN RAISE NOTICE '[Migration 000012] Rolling back organization and agent_share tables...'; END $$;

-- Rollback 000014: tenant_disabled_shared_agents first (no FK to organizations)
DROP INDEX IF EXISTS idx_tenant_disabled_shared_agents_tenant_id;
DROP TABLE IF EXISTS tenant_disabled_shared_agents;

-- Rollback 000012/000013: organization-related tables (order matters for FK)
DROP TABLE IF EXISTS agent_shares;
DROP TABLE IF EXISTS organization_join_requests;
DROP TABLE IF EXISTS kb_shares;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organizations;

DO $$ BEGIN RAISE NOTICE '[Migration 000012] Rollback completed successfully!'; END $$;
