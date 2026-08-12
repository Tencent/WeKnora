-- 将历史 extract_config.enabled 一次性迁移到唯一图谱状态源。
-- 迁移完成后，运行时代码只读取 indexing_strategy.graph_enabled。
UPDATE knowledge_bases
SET indexing_strategy = jsonb_set(
    COALESCE(
        indexing_strategy,
        '{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'::jsonb
    ),
    '{graph_enabled}',
    'true'::jsonb,
    true
)
WHERE COALESCE((extract_config ->> 'enabled')::boolean, false) = true
  AND COALESCE((indexing_strategy ->> 'graph_enabled')::boolean, false) = false;
