UPDATE custom_agents
SET config = JSON_SET(
    JSON_REMOVE(config, '$.question_suggestions'),
    '$.suggested_prompts',
    COALESCE(
        JSON_EXTRACT(config, '$.question_suggestions.starters.items'),
        JSON_ARRAY()
    )
)
WHERE config IS NOT NULL
  AND JSON_CONTAINS_PATH(config, 'one', '$.question_suggestions');

DROP TABLE IF EXISTS message_suggestion_events;
DROP TABLE IF EXISTS message_suggestion_sets;

DROP INDEX idx_messages_agent_id ON messages;
ALTER TABLE messages
    DROP COLUMN execution_context,
    DROP COLUMN model_id,
    DROP COLUMN agent_tenant_id,
    DROP COLUMN agent_id;
