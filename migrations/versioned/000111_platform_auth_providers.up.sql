-- Settings → 系统管理 → 用户管理 (本地用户 / 通用OIDC / LDAP)
--
-- 1. platform_auth_providers — one row per platform-level login provider
--    (kind = 'oidc' | 'ldap'). The non-secret settings live in `config`
--    (jsonb); the credential (OIDC client_secret / LDAP bind password) lives
--    in `secret`, AES-256-GCM wrapped when SYSTEM_AES_KEY is set. The
--    credential is never returned by an API.
--
--    Before this table existed, OIDC was driven purely by the OIDC_AUTH_*
--    environment variables. Resolution is now DB → ENV → default, so a
--    deployment that never opens the new screen keeps its previous behaviour.
--
-- 2. users.auth_source — how a local account authenticates: 'local' (a
--    password the user actually holds), 'oidc' or 'ldap' (auto-provisioned by
--    an external provider, whose local password hash is random and unknown to
--    the user). It drives the badge and the reset-password affordance in the
--    本地用户 tab, and the login path uses the same distinction to decide
--    whether a failed local password may fall through to the directory.

CREATE TABLE IF NOT EXISTS platform_auth_providers (
    id         BIGSERIAL    PRIMARY KEY,
    kind       VARCHAR(16)  NOT NULL,
    enabled    BOOLEAN      NOT NULL DEFAULT false,
    config     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    secret     TEXT         NOT NULL DEFAULT '',
    updated_by VARCHAR(36)  NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT CURRENT_TIMESTAMP
);

COMMENT ON TABLE  platform_auth_providers            IS 'Platform-level login providers managed from Settings → 系统管理 → 用户管理';
COMMENT ON COLUMN platform_auth_providers.kind       IS 'oidc | ldap';
COMMENT ON COLUMN platform_auth_providers.config     IS 'Non-secret provider settings as JSON';
COMMENT ON COLUMN platform_auth_providers.secret     IS 'Write-only credential (AES-256-GCM when SYSTEM_AES_KEY is set)';
COMMENT ON COLUMN platform_auth_providers.updated_by IS 'users.id of the system administrator who last saved this row';

CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_auth_providers_kind
    ON platform_auth_providers (kind);

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS auth_source VARCHAR(16) NOT NULL DEFAULT 'local';

COMMENT ON COLUMN users.auth_source IS
    'local | oidc | ldap — how this account authenticates';
