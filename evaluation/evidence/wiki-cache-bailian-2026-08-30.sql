-- Read-only reproduction query for wiki-cache-bailian-2026-08-30.json.
-- It intentionally contains no prompt/response columns and no mutation SQL.
WITH calls AS (
    SELECT *
    FROM evaluation_model_calls
    WHERE model_name = 'qwen3.7-plus'
      AND purpose = 'wiki_page_modify'
      AND created_at >= TIMESTAMPTZ '2026-08-29 17:32:32+00'
      AND created_at <= TIMESTAMPTZ '2026-08-29 17:33:00+00'
)
SELECT
    CASE WHEN cache_read_tokens > 0 THEN 'warm_hit' ELSE 'cold_miss' END AS cohort,
    COUNT(*) AS calls,
    SUM(prompt_tokens) AS prompt_tokens,
    SUM(completion_tokens) AS completion_tokens,
    SUM(cache_read_tokens) AS cache_read_tokens,
    SUM(cache_miss_tokens) AS cache_miss_tokens,
    ROUND(
        SUM(cache_read_tokens)::numeric /
        NULLIF(SUM(cache_read_tokens + cache_miss_tokens), 0),
        4
    ) AS token_cache_hit_rate,
    ROUND(AVG(duration_ms), 2) AS average_duration_ms,
    ROUND(SUM(estimated_cost)::numeric, 8) AS estimated_cost_cny,
    ROUND(
        (SUM(estimated_cost) * 1000 / NULLIF(SUM(prompt_tokens), 0))::numeric,
        8
    ) AS cost_per_1000_prompt_tokens_cny
FROM calls
GROUP BY cohort
ORDER BY cohort;

SELECT
    COUNT(*) AS wiki_pages,
    COUNT(*) FILTER (WHERE status = 'published') AS published_pages,
    MIN(created_at) AS first_page,
    MAX(updated_at) AS last_update
FROM wiki_pages
WHERE knowledge_base_id = 'e8e60ff5-7c2f-4948-831e-13fed2652360'
  AND deleted_at IS NULL;
