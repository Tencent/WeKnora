-- Migration: 000106_messages_session_created_index
-- Agent history pages a session backwards by (created_at, id) and looks up its
-- newest context checkpoint on every agent turn. messages was indexed by
-- session_id alone, so each page sorted every message of the session. This
-- index serves the backwards read, the newest-checkpoint lookup (a backward
-- scan that stops at the first checkpoint), and the existing recent-messages
-- reads.
--
-- CONCURRENTLY keeps writes to messages flowing while the index builds. It
-- cannot run inside a transaction block, so this file must hold exactly this
-- one statement: golang-migrate sends a file as one query, and a query with
-- several statements runs as an implicit transaction. If the build is
-- interrupted it leaves an INVALID index behind; drop it and rerun.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_messages_session_created_id
    ON messages (session_id, created_at DESC, id DESC);
