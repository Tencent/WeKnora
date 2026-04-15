-- Description: Add credentials column to tenants for third-party provider credentials (e.g. WeKnoraCloud AppID/AppSecret).
DO $$ BEGIN RAISE NOTICE '[Migration 000035] Adding credentials column to tenants'; END $$;

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS credentials JSONB DEFAULT NULL;
COMMENT ON COLUMN tenants.credentials IS 'Third-party provider credentials (e.g. WeKnoraCloud AppID/AppSecret); encrypted at application level';
