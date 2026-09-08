# Model Usage — Final Reconciliation Status

- **Repository**: `qyc060615/WeKnora` · Branch `feat/topic3-evaluation`
- **Commit**: `cd21908a652d1330499986e191fbd704b7d9c13b`
- **Tenant**: `10000`
- **Window**: `2026-08-31T13:00:00Z` → `2026-09-07T11:00:00Z` (covers Benchmark ×3, Embedding Cache, Regression, Wiki A/B)
- **Cost**: out of scope (the `model_pricing` table is empty in this environment, so all usage is `unpriced`). No Cost UI was developed.

## 1. Calls

| model | call_type | calls (DB) | calls (Analytics API) | match |
|---|---|---:|---:|---|
| deepseek-v4-pro | chat | 658 | 658 | ✓ |
| text-embedding-v4 | embedding | 217 | 217 | ✓ |
| qwen3-rerank | rerank | 136 | 136 | ✓ |
| **total** | | **1011** | **1011** | ✓ |

`Calls` semantics: one logical `model_usage` row = one call. The Analytics API
aggregates the same rows (`COUNT(*)`), and the Dashboard renders the API response, so
the three surfaces share one denominator.

## 2. Tokens & Latency

| model | input_tokens (sum / observed) | output_tokens (sum) | total_tokens (sum) | latency_ms (sum) |
|---|---|---|---:|---:|---:|
| deepseek-v4-pro (chat) | 1 689 294 / 629 | 1 409 722 | 3 099 016 | 23 589 191 |
| text-embedding-v4 (embedding) | 19 619 / 67 | null | 19 619 | 1 442 071 |
| qwen3-rerank (rerank) | null | null | 313 330 | 645 999 |

- `chat` has 29 rows with `input_tokens = NULL` (the provider-timeout retry rows
  recorded during benchmark and wiki B). These are **not** treated as 0; `observed`
  counts only non-null values.
- `rerank` and `embedding` have no input/output token split by design; `total_tokens`
  still carries their totals. `null` here is "unobserved / not applicable", never 0.

## 3. Prompt Cache (provider, `call_type = chat`)

| metric | value | derivation |
|---|---:|---|
| hit_calls | 562 | `prompt_cache_status = 'hit'` |
| miss_calls | 67 | `prompt_cache_status = 'miss'` |
| unsupported_calls | 0 | |
| unreported_calls | 0 | |
| not_recorded_calls | 29 | `prompt_cache_status IS NULL` (timeout rows) |
| eligible_calls | 629 | hit + miss |
| **Prompt Cache Call Hit Rate** | **0.893482** | `562 / 629` |
| cache_read_tokens | 913 280 | |
| cache_write_tokens | 0 | DeepSeek automatic prefix cache reports no explicit writes |
| cache_miss_tokens | 776 014 | |
| token_denominator | 1 689 294 | `SUM(input_tokens)` over hit/miss rows with both `input_tokens` and `cache_read_tokens` non-null |
| **Prompt Token Cache Ratio** | **0.540628** | `913 280 / 1 689 294` |

- The 29 `not_recorded` (NULL-status timeout) rows are excluded from the hit-rate
  denominator, per the metric contract (`NULL != 0`, unsupported/unreported are not
  counted into the denominator).

## 4. Embedding Cache (`call_type = embedding`)

| metric | value | derivation |
|---|---:|---|
| full_hit_calls | 17 | `embedding_cache_status = 'full_hit'` |
| partial_calls | 0 | |
| miss_calls | 17 | `embedding_cache_status = 'miss'` |
| disabled_calls | 183 | cache disabled rows (Embedding Cache OFF arms) |
| cache_hits (inputs) | 79 | `SUM(cache_hits)` over full_hit/partial/miss rows |
| cache_misses (inputs) | 79 | `SUM(cache_misses)` over full_hit/partial/miss rows |
| eligible_inputs | 158 | hits + misses |
| **Embedding Cache Input Hit Rate** | **0.5** | `79 / 158` |

## 5. Cross-surface status

1. **DB aggregation — PASS**: re-running the repository's aggregation SQL against the
   DB reproduces the derived metrics above (`call_hit_rate`, `token_cache_ratio`,
   `input_hit_rate`).
2. **Analytics API contract — deterministic tests PASS; runtime capture INCOMPLETE**:
   the API repository/service/handler tests pass, but no raw authenticated API response
   is present in this evidence directory. Two attempts to capture one during final
   continuation did not execute because permission review timed out.
3. **Dashboard mapping — deterministic tests PASS; authenticated runtime INCOMPLETE**:
   the dashboard component
   [`ModelUsageAnalytics.vue`](../../../frontend/src/views/settings/components/ModelUsageAnalytics.vue)
   and [`modelUsageAnalytics.ts`](../../../frontend/src/api/modelUsageAnalytics.ts) call
   `GET /api/v1/model-usage/analytics` with no independent data source. The targeted
   mapping/formatting suite passes 14/14, but no authenticated UI capture was made.
4. **Nullable fields**: not every field has a value for every model (rerank
   input/output, embedding output, chat timeout input). These are reported as
   `null`/`—`, not fabricated as zero.

**Conclusion**: the DB snapshot and both software contracts agree by deterministic
tests. Full runtime DB/API/Dashboard reconciliation remains **INCOMPLETE** until a raw
authenticated API response and dashboard capture are collected.
