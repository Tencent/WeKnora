-- Migration: 000103_message_artifacts_table
-- Skill-generated files move out of the messages.artifacts JSONB column into
-- their own table so they can be listed across sessions (the artifact library)
-- without expanding every message's JSON. The table becomes the only source of
-- truth; messages.artifacts is left in place, no longer written, so a rollback
-- can restore it from this table.
DO $$ BEGIN RAISE NOTICE '[Migration 000103] Creating message_artifacts table'; END $$;

CREATE TABLE IF NOT EXISTS message_artifacts (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    position INTEGER NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    file_name TEXT NOT NULL DEFAULT '',
    file_type VARCHAR(32) NOT NULL DEFAULT '',
    file_size BIGINT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    source_path TEXT NOT NULL DEFAULT '',
    mod_time VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_artifacts_message_position
    ON message_artifacts (message_id, position);
CREATE INDEX IF NOT EXISTS idx_message_artifacts_session_created
    ON message_artifacts (session_id, created_at);

COMMENT ON TABLE message_artifacts IS 'Files a skill script produced during an assistant turn, one row per file';
COMMENT ON COLUMN message_artifacts.position IS 'Zero-based position within the owning message; the download API addresses files by it';
COMMENT ON COLUMN message_artifacts.url IS 'Storage or resource:// URL; never returned to clients';
COMMENT ON COLUMN message_artifacts.mod_time IS 'Sandbox mtime as RFC 3339 text; kept at nanosecond precision because the collector matches files on it';
COMMENT ON COLUMN message_artifacts.source_path IS 'Absolute path inside the sandbox; repeated paths in a session are versions of one file';

DO $$ BEGIN RAISE NOTICE '[Migration 000103] Backfilling message_artifacts from messages.artifacts'; END $$;

INSERT INTO message_artifacts (
    session_id, message_id, position, url, file_name, file_type, file_size,
    content_hash, source_path, mod_time, created_at
)
SELECT
    m.session_id,
    m.id,
    (a.ord - 1)::INTEGER,
    COALESCE(a.elem ->> 'url', ''),
    COALESCE(a.elem ->> 'file_name', ''),
    LEFT(COALESCE(a.elem ->> 'file_type', ''), 32),
    COALESCE((a.elem ->> 'file_size')::BIGINT, 0),
    LEFT(COALESCE(a.elem ->> 'content_hash', ''), 64),
    COALESCE(a.elem ->> 'source_path', ''),
    LEFT(COALESCE(a.elem ->> 'mod_time', ''), 64),
    COALESCE(NULLIF(a.elem ->> 'created_at', '')::TIMESTAMPTZ, m.created_at, CURRENT_TIMESTAMP)
FROM messages m
CROSS JOIN LATERAL jsonb_array_elements(
    CASE WHEN jsonb_typeof(m.artifacts) = 'array' THEN m.artifacts ELSE '[]'::jsonb END
) WITH ORDINALITY AS a(elem, ord)
WHERE jsonb_typeof(a.elem) = 'object'
ON CONFLICT (message_id, position) DO NOTHING;
