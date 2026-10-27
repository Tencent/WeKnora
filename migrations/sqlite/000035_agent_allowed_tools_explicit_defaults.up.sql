-- Mirrors postgres migration 000116: an empty allowed_tools list now means
-- no built-in tools, so smart-reasoning agents that stored one get the
-- default tools (DefaultAllowedTools() as of this migration) written in.
-- See the postgres migration for how this narrows an "all" knowledge base
-- scope to the bases search_knowledge can use.
UPDATE custom_agents
SET config = json_set(
        config,
        '$.allowed_tools',
        json('["search_knowledge", "read_document", "list_documents", "search_conversations"]'))
WHERE json_extract(config, '$.agent_mode') = 'smart-reasoning'
  AND json_type(config, '$.allowed_tools') = 'array'
  AND json_array_length(config, '$.allowed_tools') = 0;
