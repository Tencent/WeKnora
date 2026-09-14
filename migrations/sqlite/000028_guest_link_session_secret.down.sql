-- Migration: 000012_guest_link_session_secret (down)

ALTER TABLE guest_link_channels DROP COLUMN session_secret;
