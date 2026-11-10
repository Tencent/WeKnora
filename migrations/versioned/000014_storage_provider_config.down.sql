-- Rollback: copy provider back to cos_config if needed, then drop new column
UPDATE knowledge_bases
SET cos_config = jsonb_set(
    COALESCE(cos_config, '{}'::jsonb),
    '{provider}',
    to_jsonb(storage_provider_config->>'provider')
)
WHERE storage_provider_config IS NOT NULL
  AND storage_provider_config->>'provider' IS NOT NULL
  AND storage_provider_config->>'provider' != ''
  AND storage_provider_config->>'provider' != '__pending_env__'
  AND (cos_config IS NULL
       OR cos_config->>'provider' IS NULL
       OR cos_config->>'provider' = '');

ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS storage_provider_config;
