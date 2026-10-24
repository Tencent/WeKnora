CREATE TABLE IF NOT EXISTS model_defaults (
    tenant_id BIGINT NOT NULL,
    model_type VARCHAR(50) NOT NULL,
    model_id VARCHAR(64) NOT NULL,
    PRIMARY KEY (tenant_id, model_type)
);

-- Freeze each existing workspace's effective legacy choice before the
-- application starts reading only model_defaults. Legacy readers preferred an
-- active is_default model, then the first active visible model; they did not
-- prefer tenant-owned defaults over builtin defaults. The old list had no
-- ORDER BY, so resolve ambiguous histories by creation time and ID.
WITH ranked_models AS (
    SELECT
        t.id AS tenant_id,
        m.type AS model_type,
        m.id AS model_id,
        ROW_NUMBER() OVER (
            PARTITION BY t.id, m.type
            ORDER BY
                CASE WHEN m.is_default THEN 0 ELSE 1 END,
                m.created_at ASC NULLS LAST,
                m.id ASC
        ) AS choice_rank
    FROM tenants t
    JOIN models m ON (m.tenant_id = t.id OR m.is_builtin = TRUE)
    WHERE t.deleted_at IS NULL
      AND m.deleted_at IS NULL
      AND m.status = 'active'
)
INSERT INTO model_defaults (tenant_id, model_type, model_id)
SELECT tenant_id, model_type, model_id
FROM ranked_models
WHERE choice_rank = 1
ON CONFLICT (tenant_id, model_type) DO NOTHING;
