-- Migration: 000084_guest_link_session_secret (down)
ALTER TABLE guest_link_channels DROP COLUMN IF EXISTS session_secret;
