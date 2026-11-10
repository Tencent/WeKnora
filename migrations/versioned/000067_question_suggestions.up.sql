-- Migration: 000067_question_suggestions
-- Description: Persist per-message execution context and attributable follow-up suggestions.

DO $$ BEGIN RAISE NOTICE '[Migration 000067] Adding message execution context...'; END $$;

ALTER TABLE messages
    ADD COLUMN IF NOT EXISTS agent_id VARCHAR(36) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS agent_tenant_id INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS model_id VARCHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS execution_context JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_messages_agent_id ON messages(agent_id);

CREATE TABLE IF NOT EXISTS message_suggestion_sets (
    id VARCHAR(36) PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    session_id VARCHAR(36) NOT NULL,
    assistant_message_id VARCHAR(36) NOT NULL,
    agent_id VARCHAR(36) NOT NULL DEFAULT '',
    agent_tenant_id INTEGER NOT NULL DEFAULT 0,
    placement VARCHAR(32) NOT NULL,
    config_hash VARCHAR(64) NOT NULL,
    locale VARCHAR(16) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    allow_regenerate BOOLEAN NOT NULL DEFAULT FALSE,
    suppression_reason VARCHAR(64) NOT NULL DEFAULT '',
    questions JSONB NOT NULL DEFAULT '[]'::jsonb,
    model_id VARCHAR(64) NOT NULL DEFAULT '',
    prompt_tokens INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    lease_until TIMESTAMP WITH TIME ZONE,
    generated_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_message_suggestion_sets_cache_key
    ON message_suggestion_sets (
        tenant_id,
        assistant_message_id,
        placement,
        config_hash,
        locale
    );
CREATE INDEX IF NOT EXISTS idx_message_suggestion_sets_session
    ON message_suggestion_sets(tenant_id, session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_sets_status
    ON message_suggestion_sets(status, lease_until);

CREATE TABLE IF NOT EXISTS message_suggestion_events (
    id BIGSERIAL PRIMARY KEY,
    tenant_id INTEGER NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    session_id VARCHAR(36) NOT NULL,
    suggestion_set_id VARCHAR(36) NOT NULL REFERENCES message_suggestion_sets(id) ON DELETE CASCADE,
    question_id VARCHAR(64) NOT NULL DEFAULT '',
    event_type VARCHAR(32) NOT NULL,
    actor_id VARCHAR(512) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_message_suggestion_events_set
    ON message_suggestion_events(suggestion_set_id, created_at);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_events_session
    ON message_suggestion_events(tenant_id, session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_message_suggestion_events_type
    ON message_suggestion_events(event_type, created_at);

-- Promote the legacy starter-only field into the unified agent-owned policy.
-- Follow-up generation remains off for existing agents until an owner opts in.
UPDATE custom_agents
SET config = (config - 'suggested_prompts') || jsonb_build_object(
    'question_suggestions',
    COALESCE(
        config->'question_suggestions',
        jsonb_build_object(
            'starters', jsonb_build_object(
                'enabled', true,
                'mode', 'hybrid',
                'items', COALESCE(config->'suggested_prompts', '[]'::jsonb),
                'count', 6
            ),
            'follow_ups', jsonb_build_object(
                'enabled', false,
                'mode', 'hybrid',
                'count', 3,
                'categories', jsonb_build_array('clarify', 'deepen', 'action'),
                'max_context_turns', 2,
                'suppress_on_fallback', true,
                'suppress_when_answer_asks_question', true,
                'knowledge_fallback', true,
                'allow_regenerate', false
            )
        )
    )
)
WHERE config IS NOT NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000067] Question suggestions ready'; END $$;
