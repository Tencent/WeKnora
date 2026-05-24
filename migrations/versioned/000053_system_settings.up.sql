-- Migration: 000053_system_settings
-- Adds a system-scoped (NOT tenant-scoped) settings table for platform-wide
-- runtime tunables, gated by SystemAdmin in P1.
--
-- Scope:
--   - P1 ships the schema, the 3-tier resolver (DB > ENV > built-in default),
--     and a single seeded key (file.max_size_mb) as a worked example. Adding
--     more keys is purely a service-layer registry change — no further
--     migrations needed.
--   - is_secret / requires_restart columns exist now but are wired to
--     `false` for every P1 row. P3 turns them into real semantics
--     (mask + reveal flow / "needs restart" UI badge).
--
-- Why JSONB for `value`?
--   We want to support int / string / bool / arrays / objects under one
--   schema without a separate table per type. The `value_type` column tells
--   the service layer how to parse the raw JSON. Booleans roundtrip as
--   `true`/`false`, ints as `42`, strings as `"foo"`.
--
-- Indexes:
--   - UNIQUE on (key) — primary lookup pattern, every Get hits this
--   - (category) — for the management UI's grouped list view

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

-- Seed the P1 / P3 worked examples. ON CONFLICT DO NOTHING so re-running
-- the migration on an instance where the operator already tweaked the
-- value via UI doesn't reset it.
--
-- Categories drive the management UI grouping:
--   limits   = quota / size knobs
--   security = SSRF whitelist, future ACLs
--   auth     = registration mode etc.
INSERT INTO system_settings (key, value, value_type, category, description)
VALUES
    ('file.max_size_mb',
     '50',
     'int',
     'limits',
     '上传文件大小上限（MB）。修改后立即对下次上传生效，无需重启服务。'),
    ('ssrf.whitelist',
     '[]',
     'string_list',
     'security',
     'SSRF 防护白名单。可填入 example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1。修改后立即生效。SSRF_WHITELIST_EXTRA 环境变量仍由部署方维护，不在此处覆盖。'),
    ('auth.registration_mode',
     '"self_serve"',
     'string',
     'auth',
     '自助注册模式。self_serve = 任何人可注册账号；invite_only = 关闭公网注册，仅 Owner/Admin 可邀请。修改后立即生效。')
ON CONFLICT (key) DO NOTHING;

DO $$ BEGIN RAISE NOTICE '[Migration 000053] system_settings table ready'; END $$;
