-- Migration: 000012_organizations_searchable
-- Description: Add searchable flag to organizations (open for discovery)
DO $$ BEGIN RAISE NOTICE '[Migration 000012] Adding searchable to organizations...'; END $$;

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS searchable BOOLEAN NOT NULL DEFAULT FALSE;
COMMENT ON COLUMN organizations.searchable IS 'When true, space appears in search and can be joined by org ID';

DO $$ BEGIN RAISE NOTICE '[Migration 000012] Organizations searchable column added.'; END $$;
