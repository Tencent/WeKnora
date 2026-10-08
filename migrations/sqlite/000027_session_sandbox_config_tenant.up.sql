-- Mirrors versioned migration 000108_session_sandbox_config_tenant: the pinned
-- sandbox config resolves in exactly one workspace, so the pin records which.
--
-- No backfill here. Lite has no shared spaces (see docs/LITE.md), so a session
-- can never be pinned to another workspace's config; the column exists only so
-- the shared Session model maps on both engines. INTEGER on SQLite is 64-bit
-- and matches Postgres BIGINT used in 000108.
ALTER TABLE sessions ADD COLUMN sandbox_config_tenant_id INTEGER NOT NULL DEFAULT 0;
