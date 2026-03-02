-- Description: Add storage_provider_config column to knowledge_bases, migrating from legacy cos_config.
-- This separates the storage provider selection (KB-level) from storage credentials (tenant-level).
DO $$ BEGIN RAISE NOTICE '[Migration 000014] Adding storage_provider_config to knowledge_bases'; END $$;

-- Step 1: Add new column
ALTER TABLE knowledge_bases ADD COLUMN IF NOT EXISTS storage_provider_config JSONB DEFAULT NULL;
COMMENT ON COLUMN knowledge_bases.storage_provider_config IS 'Storage provider config for this KB. Only stores provider name; credentials come from tenant StorageEngineConfig.';

-- Step 2: Migrate existing provider from legacy cos_config
UPDATE knowledge_bases
SET storage_provider_config = jsonb_build_object('provider', cos_config->>'provider')
WHERE cos_config IS NOT NULL
  AND cos_config->>'provider' IS NOT NULL
  AND cos_config->>'provider' != ''
  AND (storage_provider_config IS NULL
       OR storage_provider_config->>'provider' IS NULL
       OR storage_provider_config->>'provider' = '');

-- Step 3: For KBs that have documents but no provider set, mark them with the
-- sentinel value so the application can fill in the actual STORAGE_TYPE on startup.
-- We use 'pending_migration' as a marker; the app replaces it with the real env value.
UPDATE knowledge_bases kb
SET storage_provider_config = '{"provider": "__pending_env__"}'
WHERE (kb.storage_provider_config IS NULL
       OR kb.storage_provider_config->>'provider' IS NULL
       OR kb.storage_provider_config->>'provider' = '')
  AND EXISTS (
    SELECT 1 FROM knowledges k
    WHERE k.knowledge_base_id = kb.id
      AND k.deleted_at IS NULL
  );
