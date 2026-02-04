-- Migration: 000012_organizations (down, merged 000012, 000013, 000014)
DO $$ BEGIN RAISE NOTICE '[Migration 000012] Rolling back organization and agent_share tables...'; END $$;

DROP TABLE IF EXISTS agent_shares;
DROP TABLE IF EXISTS organization_join_requests;
DROP TABLE IF EXISTS kb_shares;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organizations;

DO $$ BEGIN RAISE NOTICE '[Migration 000012] Rollback completed successfully!'; END $$;
