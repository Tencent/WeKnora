-- 将历史 extract_config.enabled 一次性迁移到唯一图谱状态源。
-- 迁移完成后，运行时代码只读取 indexing_strategy.graph_enabled。
UPDATE knowledge_bases
SET indexing_strategy = json_set(
    COALESCE(
        indexing_strategy,
        '{"vector_enabled":true,"keyword_enabled":true,"wiki_enabled":false,"graph_enabled":false}'
    ),
    '$.graph_enabled',
    json('true')
)
WHERE COALESCE(json_extract(extract_config, '$.enabled'), 0) IN (1, 'true')
  AND COALESCE(json_extract(indexing_strategy, '$.graph_enabled'), 0) IN (0, 'false');
