UPDATE custom_agents
SET config = (config - 'question_suggestions') || jsonb_build_object(
    'suggested_prompts',
    COALESCE(config->'question_suggestions'->'starters'->'items', '[]'::jsonb)
)
WHERE config ? 'question_suggestions';

DROP TABLE IF EXISTS message_suggestion_events;
DROP TABLE IF EXISTS message_suggestion_sets;

DROP INDEX IF EXISTS idx_messages_agent_id;
ALTER TABLE messages
    DROP COLUMN IF EXISTS execution_context,
    DROP COLUMN IF EXISTS model_id,
    DROP COLUMN IF EXISTS agent_tenant_id,
    DROP COLUMN IF EXISTS agent_id;
