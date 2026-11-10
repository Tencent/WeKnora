-- Description: Skills installed onto a sandbox config, plus the snapshot chain
-- ledger. Skills live inside the config's snapshot image; these tables are the
-- metadata projection and the audit trail for the provider-side snapshots.
DO $$ BEGIN RAISE NOTICE '[Migration 000086] Creating tenant_skills'; END $$;

CREATE TABLE IF NOT EXISTS tenant_skills (
    id                    VARCHAR(36)  PRIMARY KEY,
    tenant_id             BIGINT       NOT NULL,
    sandbox_config_id     VARCHAR(36)  NOT NULL,
    name                  VARCHAR(255) NOT NULL,
    version               VARCHAR(64),
    description           TEXT,
    instructions          TEXT,
    bundle_ref            VARCHAR(1024),
    bundle_sha256         VARCHAR(64),
    enabled               BOOLEAN      NOT NULL DEFAULT TRUE,
    installed_snapshot_id VARCHAR(255),
    status                VARCHAR(32)  NOT NULL,
    error                 TEXT,
    installing_since      TIMESTAMPTZ,
    created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at            TIMESTAMPTZ
);

COMMENT ON COLUMN tenant_skills.name IS 'Also the directory name inside the image: /opt/weknora/tenant/skills/<name>';
COMMENT ON COLUMN tenant_skills.enabled IS 'Visibility to the agent only; files stay in the image until the skill is removed';

-- The unique index doubles as the "list skills of a config" index: its leftmost
-- prefix is sandbox_config_id. status/enabled are low-cardinality and are
-- filtered in memory (a config holds tens of skills, not millions).
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_skills_config_name
    ON tenant_skills (sandbox_config_id, name) WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000086] Creating tenant_skill_snapshots'; END $$;

CREATE TABLE IF NOT EXISTS tenant_skill_snapshots (
    id                  VARCHAR(36)  PRIMARY KEY,
    tenant_id           BIGINT       NOT NULL,
    sandbox_config_id   VARCHAR(36)  NOT NULL,
    skill_id            VARCHAR(36),
    snapshot_id         VARCHAR(255),
    parent_snapshot_id  VARCHAR(255),
    generation          INTEGER      NOT NULL DEFAULT 0,
    trigger             VARCHAR(16)  NOT NULL,
    state               VARCHAR(16)  NOT NULL,
    superseded_at       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE tenant_skill_snapshots IS 'Image chain ledger. Old snapshots are kept (state=superseded), never deleted on switch, so the recorded IDs stay resolvable.';

CREATE INDEX IF NOT EXISTS idx_tenant_skill_snapshots_config
    ON tenant_skill_snapshots (sandbox_config_id);
CREATE INDEX IF NOT EXISTS idx_tenant_skill_snapshots_state
    ON tenant_skill_snapshots (state);
