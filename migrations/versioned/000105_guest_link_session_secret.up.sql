-- Migration: 000084_guest_link_session_secret
DO $$ BEGIN RAISE NOTICE '[Migration 000084] Adding guest_link_channels.session_secret'; END $$;

ALTER TABLE guest_link_channels
    ADD COLUMN IF NOT EXISTS session_secret VARCHAR(64) NOT NULL DEFAULT '';

-- Backfill rows created before the column existed: an empty secret would make
-- chat session handles unsignable (SignEmbedSessionHandle fails closed).
UPDATE guest_link_channels
SET session_secret = 'gls_'
    || replace(uuid_generate_v4()::text, '-', '')
    || substr(replace(uuid_generate_v4()::text, '-', ''), 1, 24)
WHERE session_secret = '';

COMMENT ON COLUMN guest_link_channels.session_secret IS
    'Server-only HMAC key binding chat sessions to this guest link; never exposed to clients';

DO $$ BEGIN RAISE NOTICE '[Migration 000084] done'; END $$;
