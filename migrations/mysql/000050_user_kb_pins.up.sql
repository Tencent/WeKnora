-- MySQL 8 translation of 000050_user_kb_pins.up.sql.
CREATE TABLE user_kb_pins (
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id, kb_id)
);
CREATE INDEX idx_user_kb_pins_user_tenant_pinned_at
    ON user_kb_pins (tenant_id, user_id, pinned_at DESC);

INSERT IGNORE INTO user_kb_pins (tenant_id, user_id, kb_id, pinned_at)
SELECT kb.tenant_id, kb.creator_id, kb.id, COALESCE(kb.pinned_at, CURRENT_TIMESTAMP)
FROM knowledge_bases kb
WHERE kb.is_pinned = TRUE
  AND kb.creator_id IS NOT NULL
  AND kb.creator_id <> '';
