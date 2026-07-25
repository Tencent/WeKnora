-- Compatibility repair for MySQL databases initialized while 000050 was a
-- no-op and base sessions defaults were omitted.
CREATE TABLE IF NOT EXISTS user_kb_pins (
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(36) NOT NULL,
    kb_id VARCHAR(36) NOT NULL,
    pinned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (tenant_id, user_id, kb_id)
);

SET @user_kb_pins_index_exists := (
    SELECT COUNT(*)
    FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name = 'user_kb_pins'
      AND index_name = 'idx_user_kb_pins_user_tenant_pinned_at'
);
SET @user_kb_pins_index_sql := IF(
    @user_kb_pins_index_exists = 0,
    'CREATE INDEX idx_user_kb_pins_user_tenant_pinned_at ON user_kb_pins (tenant_id, user_id, pinned_at DESC)',
    'SELECT 1'
);
PREPARE user_kb_pins_index_stmt FROM @user_kb_pins_index_sql;
EXECUTE user_kb_pins_index_stmt;
DEALLOCATE PREPARE user_kb_pins_index_stmt;

INSERT IGNORE INTO user_kb_pins (tenant_id, user_id, kb_id, pinned_at)
SELECT kb.tenant_id, kb.creator_id, kb.id, COALESCE(kb.pinned_at, CURRENT_TIMESTAMP)
FROM knowledge_bases kb
WHERE kb.is_pinned = TRUE
  AND kb.creator_id IS NOT NULL
  AND kb.creator_id <> '';

ALTER TABLE sessions
    MODIFY COLUMN fallback_response TEXT NOT NULL DEFAULT ('很抱歉，我暂时无法回答这个问题。'),
    MODIFY COLUMN summary_parameters JSON NOT NULL DEFAULT (JSON_OBJECT());
