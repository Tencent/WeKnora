-- Migration: 000011_organizations (down, merged 000011–000017, 000012)
DO $$ BEGIN RAISE NOTICE '[Migration 000011] Rolling back organization tables...'; END $$;

DROP TABLE IF EXISTS organization_join_requests;
DROP TABLE IF EXISTS kb_shares;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS organizations;

DO $$ BEGIN RAISE NOTICE '[Migration 000011] Rollback completed successfully!'; END $$;
