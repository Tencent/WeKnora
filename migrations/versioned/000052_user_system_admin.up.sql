-- Migration: 000052_user_system_admin
-- Adds system-level administrator flag to users table. System admins operate
-- independently of tenant-scoped roles and have platform-wide privileges.
-- This enables organization-level superuser management, separate from per-tenant
-- admin/owner roles. The IsSystemAdmin flag is indexed for efficient queries
-- on privilege checks and admin listing.

DO $$ BEGIN RAISE NOTICE '[Migration 000052] Adding users.is_system_admin column...'; END $$;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_system_admin BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_users_is_system_admin ON users (is_system_admin);

COMMENT ON COLUMN users.is_system_admin IS 'Whether the user is a system administrator (independent of tenant roles)';

DO $$ BEGIN RAISE NOTICE '[Migration 000052] Done.'; END $$;
