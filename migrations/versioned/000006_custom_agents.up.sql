-- Migration: 000006_custom_agents
-- Description: Add custom agents table for GPTs-like agent configuration and migrate tenant config to built-in agents
DO $$ BEGIN RAISE NOTICE '[Migration 000006] Starting custom agents setup...'; END $$;

-- Create custom_agents table with composite primary key (id, tenant_id)
-- This allows the same agent ID to exist for different tenants (e.g., 'builtin-normal' for each tenant)
DO $$ BEGIN RAISE NOTICE '[Migration 000006] Creating table: custom_agents'; END $$;
CREATE TABLE IF NOT EXISTS custom_agents (
    id VARCHAR(36) NOT NULL DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar VARCHAR(64),
    is_builtin BOOLEAN NOT NULL DEFAULT false,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(36),
    config JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    PRIMARY KEY (id, tenant_id)
);

-- Add indexes for custom_agents
CREATE INDEX IF NOT EXISTS idx_custom_agents_tenant_id ON custom_agents(tenant_id);
CREATE INDEX IF NOT EXISTS idx_custom_agents_is_builtin ON custom_agents(is_builtin);
CREATE INDEX IF NOT EXISTS idx_custom_agents_deleted_at ON custom_agents(deleted_at);

-- Add agent_id column to sessions table to track which agent was used
DO $$ BEGIN RAISE NOTICE '[Migration 000006] Adding agent_id column to sessions table'; END $$;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS agent_id VARCHAR(36);
CREATE INDEX IF NOT EXISTS idx_sessions_agent_id ON sessions(agent_id);

-- Helper function to unify prompt placeholders from Go template format to simple format
CREATE OR REPLACE FUNCTION unify_prompt_placeholder(input TEXT) RETURNS TEXT AS $$
DECLARE
    result TEXT := COALESCE(input, '');
    replacements TEXT[][] := ARRAY[
        -- Go template variables -> simple placeholders
        ['{{.Query}}', '{{query}}'],
        ['{{.Answer}}', '{{answer}}'],
        ['{{.CurrentTime}}', '{{current_time}}'],
        ['{{.CurrentWeek}}', '{{current_week}}'],
        ['{{.Yesterday}}', '{{yesterday}}'],
        ['{{.Contexts}}', '{{contexts}}'],
        -- Go template control structures -> simple placeholders or remove
        ['{{range .Contexts}}', '{{contexts}}'],
        -- Remove Go template syntax
        ['{{if .Contexts}}', ''],
        ['{{else}}', ''],
        ['{{.}}', '']
    ];
    r TEXT[];
BEGIN
    FOREACH r SLICE 1 IN ARRAY replacements LOOP
        result := REPLACE(result, r[1], r[2]);
    END LOOP;
    
    -- Handle {{range .Conversation}}...{{end}} block specially
    -- Replace the entire block with just {{conversation}}
    -- The pattern matches: {{range .Conversation}} followed by any content until {{end}}
    result := regexp_replace(
        result,
        '\{\{range \.Conversation\}\}[\s\S]*?\{\{end\}\}',
        '{{conversation}}',
        'g'
    );
    
    -- Clean up any remaining {{end}} tags
    result := REPLACE(result, '{{end}}', '');
    
    RETURN result;
END;
$$ LANGUAGE plpgsql;

-- Migrate tenant AgentConfig and ConversationConfig to built-in custom agents
DO $$ BEGIN RAISE NOTICE '[Migration 000006] Migrating tenant config to built-in agents...'; END $$;

-- Insert builtin-quick-answer agent for tenants with ConversationConfig
INSERT INTO custom_agents (id, name, description, avatar, is_builtin, tenant_id, config, created_at, updated_at)
SELECT 
    'builtin-quick-answer',
    '快速问答',
    '基于知识库的 RAG 问答，快速准确地回答问题',
    '💬',
    true,
    t.id,
    jsonb_build_object(
        'agent_mode', 'quick-answer',
        'system_prompt', unify_prompt_placeholder(t.conversation_config->>'prompt'),
        'context_template', unify_prompt_placeholder(t.conversation_config->>'context_template'),
        'model_id', COALESCE(t.conversation_config->>'summary_model_id', ''),
        'rerank_model_id', COALESCE(t.conversation_config->>'rerank_model_id', ''),
        'temperature', COALESCE((t.conversation_config->>'temperature')::float, 0.7),
        'max_completion_tokens', COALESCE((t.conversation_config->>'max_completion_tokens')::int, 2048),
        'max_iterations', 10,
        'allowed_tools', '[]'::jsonb,
        'reflection_enabled', false,
        'kb_selection_mode', 'all',
        'knowledge_bases', '[]'::jsonb,
        'web_search_enabled', false,
        'web_search_max_results', COALESCE((t.web_search_config->>'max_results')::int, 5),
        'multi_turn_enabled', COALESCE((t.conversation_config->>'multi_turn_enabled')::bool, true),
        'history_turns', COALESCE((t.conversation_config->>'max_rounds')::int, 5),
        'embedding_top_k', COALESCE((t.conversation_config->>'embedding_top_k')::int, 10),
        'keyword_threshold', COALESCE((t.conversation_config->>'keyword_threshold')::float, 0.3),
        'vector_threshold', COALESCE((t.conversation_config->>'vector_threshold')::float, 0.5),
        'rerank_top_k', COALESCE((t.conversation_config->>'rerank_top_k')::int, 5),
        'rerank_threshold', COALESCE((t.conversation_config->>'rerank_threshold')::float, 0.5),
        'enable_query_expansion', COALESCE((t.conversation_config->>'enable_query_expansion')::bool, true),
        'enable_rewrite', COALESCE((t.conversation_config->>'enable_rewrite')::bool, true),
        'rewrite_prompt_system', unify_prompt_placeholder(t.conversation_config->>'rewrite_prompt_system'),
        'rewrite_prompt_user', unify_prompt_placeholder(t.conversation_config->>'rewrite_prompt_user'),
        'fallback_strategy', COALESCE(t.conversation_config->>'fallback_strategy', 'model'),
        'fallback_response', unify_prompt_placeholder(t.conversation_config->>'fallback_response'),
        'fallback_prompt', unify_prompt_placeholder(t.conversation_config->>'fallback_prompt')
    ),
    NOW(),
    NOW()
FROM tenants t
WHERE t.conversation_config IS NOT NULL
  AND t.deleted_at IS NULL
ON CONFLICT (id, tenant_id) DO UPDATE SET
    config = EXCLUDED.config,
    updated_at = NOW();

-- Insert builtin-smart-reasoning agent for tenants with AgentConfig
INSERT INTO custom_agents (id, name, description, avatar, is_builtin, tenant_id, config, created_at, updated_at)
SELECT 
    'builtin-smart-reasoning',
    '智能推理',
    'ReAct 推理框架，支持多步思考和工具调用',
    '🤖',
    true,
    t.id,
    jsonb_build_object(
        'agent_mode', 'smart-reasoning',
        'system_prompt', unify_prompt_placeholder(t.agent_config->>'system_prompt_web_disabled'),
        'system_prompt_web_enabled', unify_prompt_placeholder(t.agent_config->>'system_prompt_web_enabled'),
        'context_template', '',
        'model_id', COALESCE(t.conversation_config->>'summary_model_id', ''),
        'rerank_model_id', COALESCE(t.conversation_config->>'rerank_model_id', ''),
        'temperature', COALESCE((t.agent_config->>'temperature')::float, 0.7),
        'max_completion_tokens', 2048,
        'max_iterations', COALESCE((t.agent_config->>'max_iterations')::int, 50),
        'allowed_tools', COALESCE(t.agent_config->'allowed_tools', '["thinking", "todo_write", "knowledge_search", "grep_chunks", "list_knowledge_chunks", "query_knowledge_graph", "get_document_info"]'::jsonb),
        'reflection_enabled', COALESCE((t.agent_config->>'reflection_enabled')::bool, false),
        'mcp_selection_mode', 'all',
        'mcp_services', '[]'::jsonb,
        'kb_selection_mode', 'all',
        'knowledge_bases', COALESCE(t.agent_config->'knowledge_bases', '[]'::jsonb),
        'web_search_enabled', COALESCE((t.agent_config->>'web_search_enabled')::bool, true),
        'web_search_max_results', COALESCE((t.agent_config->>'web_search_max_results')::int, COALESCE((t.web_search_config->>'max_results')::int, 5)),
        'multi_turn_enabled', COALESCE((t.agent_config->>'multi_turn_enabled')::bool, true),
        'history_turns', COALESCE((t.agent_config->>'history_turns')::int, 5),
        'embedding_top_k', 10,
        'keyword_threshold', 0.3,
        'vector_threshold', 0.5,
        'rerank_top_k', 5,
        'rerank_threshold', 0.5,
        'enable_query_expansion', false,
        'enable_rewrite', false,
        'rewrite_prompt_system', '',
        'rewrite_prompt_user', '',
        'fallback_strategy', 'model',
        'fallback_response', '',
        'fallback_prompt', ''
    ),
    NOW(),
    NOW()
FROM tenants t
WHERE t.agent_config IS NOT NULL
  AND t.deleted_at IS NULL
ON CONFLICT (id, tenant_id) DO UPDATE SET
    config = EXCLUDED.config,
    updated_at = NOW();

