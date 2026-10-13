CREATE TABLE IF NOT EXISTS toolbox_categories (
    id         VARCHAR(36) PRIMARY KEY,
    tenant_id  INTEGER     NOT NULL,
    name       VARCHAR(64) NOT NULL,
    created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_toolbox_categories_tenant_name
    ON toolbox_categories (tenant_id, name);

CREATE TABLE IF NOT EXISTS toolbox_category_skills (
    category_id      VARCHAR(36) NOT NULL,
    skill_catalog_id VARCHAR(36) NOT NULL,
    created_at       DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (category_id, skill_catalog_id),
    FOREIGN KEY (category_id) REFERENCES toolbox_categories(id) ON DELETE CASCADE,
    FOREIGN KEY (skill_catalog_id) REFERENCES tenant_skill_catalog(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_toolbox_category_skills_skill
    ON toolbox_category_skills (skill_catalog_id);

CREATE TABLE IF NOT EXISTS toolbox_category_mcp_services (
    category_id   VARCHAR(36) NOT NULL,
    mcp_service_id VARCHAR(36) NOT NULL,
    created_at     DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (category_id, mcp_service_id),
    FOREIGN KEY (category_id) REFERENCES toolbox_categories(id) ON DELETE CASCADE,
    FOREIGN KEY (mcp_service_id) REFERENCES mcp_services(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_toolbox_category_mcp_services_service
    ON toolbox_category_mcp_services (mcp_service_id);
