ALTER TABLE knowledge_processing_spans
    MODIFY COLUMN name VARCHAR(64) NOT NULL;

ALTER TABLE tenant_members
    DROP INDEX idx_tenant_members_user_tenant_unique;

ALTER TABLE tenant_members
    DROP COLUMN active_unique_key;

CREATE UNIQUE INDEX idx_tenant_members_user_tenant_unique
    ON tenant_members(user_id, tenant_id);
