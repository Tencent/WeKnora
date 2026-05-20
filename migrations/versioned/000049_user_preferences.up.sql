-- Migration: 000049_user_preferences
-- Adds a user-scoped JSON preferences blob so per-user UI toggles
-- (memory feature, future preferences) can be persisted server-side and
-- sync across devices/browsers. Previously the only carrier for such
-- toggles was the browser's localStorage, which intentionally does NOT
-- cross devices — switching machines silently reset every preference.
--
-- A single jsonb column keeps the schema flat: new keys can be added by
-- writing through the existing PUT /auth/me/preferences endpoint, no DDL
-- per knob. Server reads/writes are partial updates (key-merge), so an
-- old client that only knows about a subset of keys can't accidentally
-- wipe newer ones it doesn't understand.
--
-- Defaults to '{}' (NOT NULL) so existing rows behave as "no preference
-- set yet" without forcing every read path to handle NULL.

DO $$ BEGIN RAISE NOTICE '[Migration 000049] Adding users.preferences column...'; END $$;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS preferences JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN users.preferences IS 'Per-user JSON preferences (memory toggle, future UI knobs)';

DO $$ BEGIN RAISE NOTICE '[Migration 000049] Done.'; END $$;
