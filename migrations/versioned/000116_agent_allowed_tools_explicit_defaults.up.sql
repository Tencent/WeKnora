DO $$ BEGIN RAISE NOTICE '[Migration 000116] Writing default tools into empty agent tool lists...'; END $$;

-- An empty allowed_tools list used to select the default tools at runtime.
-- It now means no built-in tools, and only an unset list selects the
-- defaults. Smart-reasoning agents that stored an empty list relied on the
-- old meaning, so write the defaults into them explicitly and they keep the
-- tools they ran with. The list is DefaultAllowedTools() as of this
-- migration. Other modes register no tools and are left as they are.
--
-- The knowledge base filter of an agent whose scope is "all" follows the
-- stored list, so a rewritten agent now gets the same scope as one that
-- ticked the default tools explicitly: knowledge bases search_knowledge
-- cannot use (wiki-only ones) leave the scope.
UPDATE custom_agents
SET config = jsonb_set(
        config,
        '{allowed_tools}',
        '["search_knowledge", "read_document", "list_documents", "search_conversations"]'::jsonb)
WHERE config->>'agent_mode' = 'smart-reasoning'
  AND config->'allowed_tools' = '[]'::jsonb;

DO $$ BEGIN RAISE NOTICE '[Migration 000116] Empty agent tool lists rewritten'; END $$;
