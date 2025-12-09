-- Drop indexes for chunks
DROP INDEX IF EXISTS idx_chunks_tenant_kg;
DROP INDEX IF EXISTS idx_chunks_parent_id;
DROP INDEX IF EXISTS idx_chunks_chunk_type;

-- Drop chunks table
DROP TABLE IF EXISTS chunks;

-- Drop indexes for messages
DROP INDEX IF EXISTS idx_messages_session_id;

-- Drop messages table
DROP TABLE IF EXISTS messages;

-- Drop indexes for sessions
DROP INDEX IF EXISTS idx_sessions_tenant_id;

-- Drop sessions table
DROP TABLE IF EXISTS sessions;

-- Drop indexes for knowledges
DROP INDEX IF EXISTS idx_knowledges_tenant_id;
DROP INDEX IF EXISTS idx_knowledges_base_id;
DROP INDEX IF EXISTS idx_knowledges_parse_status;
DROP INDEX IF EXISTS idx_knowledges_enable_status;

-- Drop knowledges table
DROP TABLE IF EXISTS knowledges;

-- Drop indexes for knowledge_bases
DROP INDEX IF EXISTS idx_knowledge_bases_tenant_id;

-- Drop knowledge_bases table
DROP TABLE IF EXISTS knowledge_bases;

-- Drop indexes for models
DROP INDEX IF EXISTS idx_models_type;
DROP INDEX IF EXISTS idx_models_source;

-- Drop models table
DROP TABLE IF EXISTS models;

-- Drop indexes for tenants
DROP INDEX IF EXISTS idx_tenants_api_key;
DROP INDEX IF EXISTS idx_tenants_status;

-- Drop tenants table
DROP TABLE IF EXISTS tenants;

-- Note: Extensions are not dropped as they may be used by other databases/schemas
-- If you want to drop extensions, uncomment the following lines:
-- DROP EXTENSION IF EXISTS pg_search;
-- DROP EXTENSION IF EXISTS pg_trgm;
-- DROP EXTENSION IF EXISTS vector;
-- DROP EXTENSION IF EXISTS "uuid-ossp";
