-- Compatibility repair for MySQL databases initialized while 000003 was a
-- no-op translation. MySQL 8.0 does not support ADD COLUMN IF NOT EXISTS, so
-- use dynamic SQL to keep this safe for both old and freshly initialized DBs.
SET @chunk_flags_exists := (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'chunks'
      AND column_name = 'flags'
);
SET @chunk_flags_sql := IF(
    @chunk_flags_exists = 0,
    'ALTER TABLE chunks ADD COLUMN flags INTEGER NOT NULL DEFAULT 1',
    'SELECT 1'
);
PREPARE chunk_flags_stmt FROM @chunk_flags_sql;
EXECUTE chunk_flags_stmt;
DEALLOCATE PREPARE chunk_flags_stmt;

ALTER TABLE chunks
    MODIFY COLUMN flags INTEGER NOT NULL DEFAULT 1;
