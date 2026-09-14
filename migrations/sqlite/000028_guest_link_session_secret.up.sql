-- Migration: 000012_guest_link_session_secret
-- Server-only HMAC key binding chat sessions to a guest link.
-- (align with versioned 000084)

ALTER TABLE guest_link_channels ADD COLUMN session_secret VARCHAR(64) NOT NULL DEFAULT '';

UPDATE guest_link_channels
SET session_secret = 'gls_' || lower(hex(randomblob(24)))
WHERE session_secret = '';
