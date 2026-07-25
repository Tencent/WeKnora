ALTER TABLE tenant_members
    DROP INDEX idx_tenant_members_user_tenant_unique;

ALTER TABLE tenant_members
    ADD COLUMN active_unique_key TINYINT
        AS (CASE WHEN deleted_at IS NULL THEN 1 ELSE NULL END) STORED;

CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique
    ON tenant_members(user_id, tenant_id, active_unique_key);

ALTER TABLE knowledge_processing_spans
    MODIFY COLUMN name VARCHAR(255) NOT NULL;
