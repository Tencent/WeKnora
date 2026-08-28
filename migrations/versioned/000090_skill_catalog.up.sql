-- Description: Tenant-level skill catalog. A skill exists independently of any
-- sandbox; tenant_skills rows become installations onto one config's image.
DO $$ BEGIN RAISE NOTICE '[Migration 000090] Creating tenant_skill_catalog'; END $$;

CREATE TABLE IF NOT EXISTS tenant_skill_catalog (
    id            VARCHAR(36)  PRIMARY KEY,
    tenant_id     BIGINT       NOT NULL,
    name          VARCHAR(255) NOT NULL,
    version       VARCHAR(64),
    description   TEXT,
    instructions  TEXT,
    bundle_ref    VARCHAR(1024),
    bundle_sha256 VARCHAR(64),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

COMMENT ON TABLE tenant_skill_catalog IS
    'Workspace skill definition. Installations onto sandbox configs live in tenant_skills.';

CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_skill_catalog_name
    ON tenant_skill_catalog (tenant_id, name) WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000090] Linking tenant_skills to catalog'; END $$;

ALTER TABLE tenant_skills ADD COLUMN IF NOT EXISTS catalog_id VARCHAR(36);

CREATE INDEX IF NOT EXISTS idx_tenant_skills_catalog
    ON tenant_skills (catalog_id);

-- One catalog row per (tenant, name), using the oldest live install as the id
-- so existing rows keep a stable identity. Later installs of the same name
-- point at it via catalog_id.
INSERT INTO tenant_skill_catalog (
    id, tenant_id, name, version, description, instructions,
    bundle_ref, bundle_sha256, created_at, updated_at
)
SELECT DISTINCT ON (tenant_id, name)
    id, tenant_id, name, version, description, instructions,
    bundle_ref, bundle_sha256, created_at, updated_at
FROM tenant_skills
WHERE deleted_at IS NULL
ORDER BY tenant_id, name, created_at ASC
ON CONFLICT (id) DO NOTHING;

UPDATE tenant_skills AS s
SET catalog_id = c.id
FROM tenant_skill_catalog AS c
WHERE s.deleted_at IS NULL
  AND c.deleted_at IS NULL
  AND s.tenant_id = c.tenant_id
  AND s.name = c.name
  AND (s.catalog_id IS NULL OR s.catalog_id = '');
