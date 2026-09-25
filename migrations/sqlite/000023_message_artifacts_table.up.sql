-- Mirrors versioned migration 000103_message_artifacts_table:
-- skill-generated files move out of messages.artifacts into their own table,
-- which becomes the only source of truth. After the backfill the legacy column
-- is cleared (message loads still SELECT *) but kept; the down migration
-- rebuilds it from the table.

CREATE TABLE IF NOT EXISTS message_artifacts (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    position INTEGER NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    file_name TEXT NOT NULL DEFAULT '',
    file_type VARCHAR(32) NOT NULL DEFAULT '',
    file_size INTEGER NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    source_path TEXT NOT NULL DEFAULT '',
    mod_time VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_artifacts_message_position
    ON message_artifacts (message_id, position);
CREATE INDEX IF NOT EXISTS idx_message_artifacts_session_created
    ON message_artifacts (session_id, created_at);

-- mod_time is kept as the RFC 3339 text Go wrote (nanosecond precision; the
-- collector matches files on it). created_at is an ORDER BY key compared as
-- text, so it is normalised to UTC in the layout the repository writes.
INSERT OR IGNORE INTO message_artifacts (
    id, session_id, message_id, position, url, file_name, file_type, file_size,
    content_hash, source_path, mod_time, created_at
)
SELECT
    lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-' || hex(randomblob(2)) || '-' ||
          hex(randomblob(2)) || '-' || hex(randomblob(6))),
    m.session_id,
    m.id,
    CAST(a.key AS INTEGER),
    COALESCE(json_extract(a.value, '$.url'), ''),
    COALESCE(json_extract(a.value, '$.file_name'), ''),
    substr(COALESCE(json_extract(a.value, '$.file_type'), ''), 1, 32),
    COALESCE(CAST(json_extract(a.value, '$.file_size') AS INTEGER), 0),
    substr(COALESCE(json_extract(a.value, '$.content_hash'), ''), 1, 64),
    COALESCE(json_extract(a.value, '$.source_path'), ''),
    substr(COALESCE(json_extract(a.value, '$.mod_time'), ''), 1, 64),
    COALESCE(
        strftime('%Y-%m-%d %H:%M:%f+00:00', NULLIF(json_extract(a.value, '$.created_at'), '')),
        strftime('%Y-%m-%d %H:%M:%f+00:00', m.created_at),
        strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
    )
FROM messages m, json_each(
    CASE WHEN json_valid(m.artifacts) AND json_type(m.artifacts) = 'array' THEN m.artifacts ELSE '[]' END
) a
WHERE a.type = 'object';

-- Only rows that carried artifacts; '[]' rows are left as they are.
UPDATE messages SET artifacts = NULL
WHERE artifacts IS NOT NULL AND artifacts <> '[]';
