-- Migration: 000053_system_admin_and_settings
-- Two related changes for the system-admin / platform-settings feature, merged
-- into a single migration since they ship together and have no intermediate
-- consumer:
--
--   1. users.is_system_admin
--      System-level administrator flag, independent of tenant-scoped roles.
--      System admins have platform-wide privileges (e.g. editing
--      system_settings below). Indexed for efficient privilege checks and
--      admin listing.
--
--   2. system_settings table
--      System-scoped (NOT tenant-scoped) settings for platform-wide runtime
--      tunables, gated by SystemAdmin.
--
--      Deliberately do not seed values here. For migrated deployments, a DB
--      row has higher precedence than ENV, so inserting built-in defaults
--      would silently override existing operator configuration such as
--      DISABLE_REGISTRATION, SSRF_WHITELIST, and MAX_FILE_SIZE_MB. The
--      service exposes registry-backed virtual rows to the management UI
--      until an admin explicitly saves a value.
--
--      Why JSONB for `value`?
--        We want to support int / string / bool / arrays / objects under one
--        schema without a separate table per type. The `value_type` column
--        tells the service layer how to parse the raw JSON. Booleans
--        roundtrip as `true`/`false`, ints as `42`, strings as `"foo"`.
--
--      Indexes:
--        - UNIQUE on (key) — primary lookup pattern, every Get hits this
--        - (category) — for the management UI's grouped list view

DO $$ BEGIN RAISE NOTICE '[Migration 000053] Adding users.is_system_admin column...'; END $$;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_system_admin BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_users_is_system_admin ON users (is_system_admin);

COMMENT ON COLUMN users.is_system_admin IS 'Whether the user is a system administrator (independent of tenant roles)';

DO $$ BEGIN RAISE NOTICE '[Migration 000053] Creating table: system_settings'; END $$;

CREATE TABLE IF NOT EXISTS system_settings (
    id               BIGSERIAL PRIMARY KEY,
    key              VARCHAR(128) NOT NULL UNIQUE,
    value            JSONB NOT NULL,
    value_type       VARCHAR(16)  NOT NULL,
    category         VARCHAR(32)  NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    is_secret        BOOLEAN NOT NULL DEFAULT false,
    requires_restart BOOLEAN NOT NULL DEFAULT false,
    last_modified_by VARCHAR(36) NOT NULL DEFAULT '',
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_system_settings_category
    ON system_settings (category);

DO $$ BEGIN RAISE NOTICE '[Migration 000053] Done.'; END $$;
