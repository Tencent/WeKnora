-- Migration: 000013_organization_member_limit (down)
ALTER TABLE organizations
    DROP COLUMN IF EXISTS member_limit;
