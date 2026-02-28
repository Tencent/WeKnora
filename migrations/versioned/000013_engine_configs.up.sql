-- Description: Add parser_engine_config and storage_engine_config to tenants for UI-configured overrides.
DO $$ BEGIN RAISE NOTICE '[Migration 000013] Adding engine config columns to tenants'; END $$;

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS parser_engine_config JSONB DEFAULT NULL;
COMMENT ON COLUMN tenants.parser_engine_config IS 'Parser engine overrides (mineru_endpoint, mineru_api_key, mineru_api_base_url); takes precedence over env when parsing';

ALTER TABLE tenants ADD COLUMN IF NOT EXISTS storage_engine_config JSONB DEFAULT NULL;
COMMENT ON COLUMN tenants.storage_engine_config IS 'Storage engine parameters for Local, MinIO, COS; used for document/file storage and docreader';
