# Model Usage Runtime Reconciliation

## Runtime Scope

- Tenant: `10000`
- Model: `deepseek-v4-pro`
- Model ID: `5a50bf0a-60f9-4340-974b-0bd85c80b286`
- Fixed DB/API range: `[2026-08-31T13:00:00Z, 2026-09-07T11:00:00Z)`
- Dashboard selection: `2026-08-31` through `2026-09-07`, Asia/Shanghai

The Dashboard date-only control generated a wider UTC range. Read-only DB
verification confirmed that both ranges selected the same 658 DeepSeek rows, so the
effective data window and aggregates are identical at capture time.

## Reconciliation

| Metric | DB | API | Dashboard | Verdict |
|---|---:|---:|---:|---|
| Calls | 658 | 658 | 658 | PASS |
| Input tokens | 1,689,294 | 1,689,294 | 168.9万 | PASS |
| Output tokens | 1,409,722 | 1,409,722 | 141万 | PASS |
| Total tokens | 3,099,016 | 3,099,016 | 309.9万 | PASS |
| Average latency | 35,849.834347 ms | 35,849.83434650456 ms | 35.85 s | PASS |
| Prompt hit/miss/eligible | 562/67/629 | 562/67/629 | 562/67/629 | PASS |
| Prompt Call Hit Rate | 0.893482 | 0.893481717 | 89.3% | PASS |
| Prompt Token Cache Ratio | 0.540628 | 0.540628215 | 54.1% | PASS |

## Verdict

**PASS.** Runtime DB, authenticated Analytics API, and Dashboard values reconcile.
Dashboard values are formatted versions of the same underlying aggregates.

## Semantics

- `NULL != 0`; missing provider observations are not fabricated as zero.
- Calls are logical `model_usage` rows.
- Prompt Call Hit Rate = `hit / (hit + miss)`.
- Prompt Token Cache Ratio = `SUM(cache_read_tokens) / SUM(input_tokens)` over
  eligible observed rows.
- Embedding Input Hit Rate = `hits / (hits + misses)`; it is null/not applicable for
  this chat-only model filter.

## Evidence

- [artifacts/rhino_2026_final/model_usage/runtime_db_capture.txt](runtime_db_capture.txt)
- [artifacts/rhino_2026_final/model_usage/runtime_api_capture.json](runtime_api_capture.json)
- [artifacts/rhino_2026_final/model_usage/dashboard_runtime_capture.md](dashboard_runtime_capture.md)
- [artifacts/rhino_2026_final/model_usage/runtime_reconciliation.md](runtime_reconciliation.md)
- [artifacts/rhino_2026_final/model_usage/db_snapshot.json](db_snapshot.json)
- [artifacts/rhino_2026_final/model_usage/reconciliation.md](reconciliation.md)
