-- Read-only reproduction queries for embedding-cache-ollama-2026-08-30.json.
-- No prompt, document body, API key, or tenant identifier is selected.

WITH cohorts AS (
    SELECT
        CASE
            WHEN created_at >= TIMESTAMPTZ '2026-08-30 04:25:56+00'
             AND created_at <= TIMESTAMPTZ '2026-08-30 04:27:42+00' THEN 'cold'
            WHEN created_at >= TIMESTAMPTZ '2026-08-30 04:43:35+00'
             AND created_at <= TIMESTAMPTZ '2026-08-30 04:45:19+00' THEN 'warm_after_restart'
        END AS cohort,
        lookup_count,
        hit_count,
        miss_count,
        deduplicated_count,
        avoided_computations
    FROM embedding_cache_usage_events
    WHERE model_name = 'nomic-embed-text:latest'
      AND (
          created_at BETWEEN TIMESTAMPTZ '2026-08-30 04:25:56+00'
                         AND TIMESTAMPTZ '2026-08-30 04:27:42+00'
          OR created_at BETWEEN TIMESTAMPTZ '2026-08-30 04:43:35+00'
                         AND TIMESTAMPTZ '2026-08-30 04:45:19+00'
      )
)
SELECT
    cohort,
    SUM(lookup_count) AS lookup_count,
    SUM(hit_count) AS cache_hits,
    SUM(miss_count) AS provider_computations,
    SUM(deduplicated_count) AS deduplicated_count,
    SUM(avoided_computations) AS avoided_computations,
    ROUND(SUM(hit_count)::numeric / NULLIF(SUM(lookup_count), 0), 6) AS cache_hit_rate
FROM cohorts
GROUP BY cohort
ORDER BY cohort;

SELECT
    kb.name AS knowledge_base,
    k.file_name,
    k.file_hash,
    k.parse_status,
    k.created_at AT TIME ZONE 'Asia/Shanghai' AS created_at_cn,
    k.processed_at AT TIME ZONE 'Asia/Shanghai' AS processed_at_cn
FROM knowledges k
JOIN knowledge_bases kb ON kb.id = k.knowledge_base_id
WHERE kb.name IN (
    '课题三-Embedding缓存验收',
    '课题三-Embedding缓存验收-暖缓存',
    '课题三-Embedding缓存验收-持久化复验'
)
ORDER BY k.created_at;
