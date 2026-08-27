-- Description: Environment variables for skill execution. Two things live here:
-- the per-skill declaration an installer agent produces (with the optional
-- workspace-wide value an admin supplies), and the values individual members
-- keep for themselves.
--
-- A member value with an empty skill_id belongs to the whole sandbox config and
-- is injected into every execution on it; one with a skill_id is that skill's
-- declared credential, injected only when a tool names the skill. The storage is
-- shared because it is the same kind of thing — only the load timing differs.
DO $$ BEGIN RAISE NOTICE '[Migration 000088] Adding tenant_skills.envs'; END $$;

ALTER TABLE tenant_skills ADD COLUMN IF NOT EXISTS envs JSONB;

COMMENT ON COLUMN tenant_skills.envs IS
    'Installer-agent declaration [{name,description,required,value}]. value is the workspace-wide admin value and is AES-GCM encrypted; the rest is plaintext so the UI can render it without a key.';

DO $$ BEGIN RAISE NOTICE '[Migration 000088] Creating tenant_user_env_vars'; END $$;

CREATE TABLE IF NOT EXISTS tenant_user_env_vars (
    id                VARCHAR(36)  PRIMARY KEY,
    tenant_id         BIGINT       NOT NULL,
    principal_type    VARCHAR(32)  NOT NULL,
    principal_id      VARCHAR(512) NOT NULL,
    sandbox_config_id VARCHAR(36)  NOT NULL,
    skill_id          VARCHAR(36)  NOT NULL DEFAULT '',
    name              VARCHAR(255) NOT NULL,
    value             TEXT,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE tenant_user_env_vars IS
    'One principal''s own environment variable. An empty skill_id applies to every execution on the sandbox config; a skill_id scopes it to that skill''s declaration. Keyed by principal, not user_id: the IM path stores a synthetic tenant account in the user ID, which would make every IM user of a workspace share one value.';
COMMENT ON COLUMN tenant_user_env_vars.value IS 'AES-GCM encrypted. Never returned by any endpoint.';

-- Also the read index for resolution: its leftmost prefix is the exact tuple
-- looked up on every execution. sandbox_config_id is part of the key because
-- without it two configs could not each hold a config-wide variable of the
-- same name (their skill_id is empty in both).
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_env_var
    ON tenant_user_env_vars (tenant_id, principal_type, principal_id, sandbox_config_id, skill_id, name);

-- Cleanup indexes. Skills and sandbox configs are soft-deleted, so a foreign
-- key with ON DELETE CASCADE would never fire; deletion is explicit in the
-- repository and needs these to stay cheap.
CREATE INDEX IF NOT EXISTS idx_user_env_var_skill
    ON tenant_user_env_vars (tenant_id, skill_id);
CREATE INDEX IF NOT EXISTS idx_user_env_var_config
    ON tenant_user_env_vars (tenant_id, sandbox_config_id);
