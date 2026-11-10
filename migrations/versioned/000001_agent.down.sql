BEGIN;

DROP INDEX IF EXISTS idx_knowledges_summary_status;
ALTER TABLE knowledges DROP COLUMN IF EXISTS summary_status;

ALTER TABLE knowledge_bases 
DROP COLUMN IF EXISTS question_generation_config;

ALTER TABLE users
    DROP COLUMN IF EXISTS can_access_all_tenants;

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS rerank_model_id VARCHAR(64);

UPDATE models m
JOIN knowledges k ON m.id = k.id
SET m.tenant_id = 0
WHERE m.tenant_id = k.tenant_id;

-- Drop index on content_hash
DROP INDEX IF EXISTS idx_chunks_content_hash;

-- Drop content_hash column
ALTER TABLE chunks
    DROP COLUMN IF EXISTS content_hash;

-- Drop status column
ALTER TABLE chunks
    DROP COLUMN IF EXISTS status;

-- Drop indexes and columns referencing tags
DROP INDEX IF EXISTS idx_chunks_tag;
ALTER TABLE chunks DROP COLUMN IF EXISTS tag_id;

DROP INDEX IF EXISTS idx_knowledges_tag;
ALTER TABLE knowledges DROP COLUMN IF EXISTS tag_id;

-- Drop tag table
DROP INDEX IF EXISTS idx_knowledge_tags_kb_name;
DROP INDEX IF EXISTS idx_knowledge_tags_kb;
DROP TABLE IF EXISTS knowledge_tags;

ALTER TABLE chunks
  DROP COLUMN IF EXISTS metadata;

ALTER TABLE knowledge_bases
  DROP COLUMN IF EXISTS faq_config,
  DROP COLUMN IF EXISTS type;

-- Drop index
DROP INDEX IF EXISTS idx_models_is_builtin;

-- Remove is_builtin column
ALTER TABLE models 
DROP COLUMN IF EXISTS is_builtin;

-- Remove check constraint
ALTER TABLE mcp_services
DROP CONSTRAINT IF EXISTS chk_mcp_transport_config;

-- Make url column required again
ALTER TABLE mcp_services 
ALTER COLUMN url SET NOT NULL;

-- Remove stdio_config and env_vars columns
ALTER TABLE mcp_services 
DROP COLUMN IF EXISTS env_vars,
DROP COLUMN IF EXISTS stdio_config;

-- Remove web_search_config column
ALTER TABLE tenants 
DROP COLUMN IF EXISTS web_search_config;

-- Drop trigger
DROP TRIGGER IF EXISTS trigger_mcp_services_updated_at ON mcp_services;

-- Drop function
DROP FUNCTION IF EXISTS update_mcp_services_updated_at();

-- Drop indexes
DROP INDEX IF EXISTS idx_mcp_services_tenant_id;
DROP INDEX IF EXISTS idx_mcp_services_enabled;
DROP INDEX IF EXISTS idx_mcp_services_deleted_at;

-- Drop table
DROP TABLE IF EXISTS mcp_services;

-- This migration performs a data cleanup (soft delete) which is not safely reversible.
-- Down migration is a no-op.

-- Drop foreign key constraints first
ALTER TABLE auth_tokens DROP CONSTRAINT IF EXISTS fk_auth_tokens_user;
ALTER TABLE users DROP CONSTRAINT IF EXISTS fk_users_tenant;

-- Drop tables
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS users;

-- Drop is_temporary column from knowledge_bases
ALTER TABLE knowledge_bases
    DROP COLUMN IF EXISTS is_temporary;

-- Drop context_config column from tenants
ALTER TABLE tenants
    DROP COLUMN IF EXISTS context_config;

-- Drop conversation_config column from tenants
ALTER TABLE tenants
    DROP COLUMN IF EXISTS conversation_config;

-- Drop JSONB indexes if they exist
DROP INDEX IF EXISTS idx_messages_agent_steps;
DROP INDEX IF EXISTS idx_sessions_context_config;
DROP INDEX IF EXISTS idx_sessions_agent_config;
DROP INDEX IF EXISTS idx_tenants_agent_config;

-- Drop columns if they exist
ALTER TABLE messages
    DROP COLUMN IF EXISTS agent_steps;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS context_config;

ALTER TABLE sessions
    DROP COLUMN IF EXISTS agent_config;

ALTER TABLE tenants
    DROP COLUMN IF EXISTS agent_config;

COMMIT;
