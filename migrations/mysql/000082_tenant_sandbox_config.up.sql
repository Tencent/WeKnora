-- Store multiple named sandbox backend configs per workspace.
CREATE TABLE tenant_sandbox_configs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT UNSIGNED NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    sandbox_type VARCHAR(32) NOT NULL,
    config JSON NOT NULL,
    cordoned_at DATETIME(6),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6),
    live_marker CHAR(1) GENERATED ALWAYS AS (
        CASE WHEN deleted_at IS NULL THEN '1' ELSE NULL END
    ) VIRTUAL,
    KEY idx_tenant_sandbox_configs_tenant (tenant_id, deleted_at),
    UNIQUE KEY uq_tenant_sandbox_configs_tenant_name (tenant_id, name, live_marker)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
