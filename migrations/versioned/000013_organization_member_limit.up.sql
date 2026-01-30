-- Migration: 000013_organization_member_limit
-- Description: Add member_limit to organizations (0 = unlimited)
ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS member_limit INTEGER NOT NULL DEFAULT 50;

COMMENT ON COLUMN organizations.member_limit IS 'Max members allowed; 0 means no limit';
