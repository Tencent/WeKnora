# Model Usage Runtime Reconciliation

## Scope

- Tenant: `10000`
- Model: `deepseek-v4-pro`
- Model ID: `5a50bf0a-60f9-4340-974b-0bd85c80b286`
- Fixed DB/API range: `[2026-08-31T13:00:00Z, 2026-09-07T11:00:00Z)`
- Call type selected by this model: `chat`

## DB -> Analytics API -> Dashboard

| Metric | DB runtime | Analytics API raw | Dashboard screenshot | Verdict |
|---|---:|---:|---:|---|
| Calls | 658 | 658 | 658 | PASS |
| Input tokens | 1,689,294 | 1,689,294 | 168.9万 | PASS |
| Output tokens | 1,409,722 | 1,409,722 | 141万 | PASS |
| Total tokens | 3,099,016 | 3,099,016 | 309.9万 | PASS |
| Input observed/applicable | 629 / 658 | 629 / 658 | 629 / 658 | PASS |
| Latency sum (ms) | 23,589,191 | 23,589,191 | — | PASS DB/API |
| Average latency | 35,849.834347 ms | 35,849.83434650456 ms | 35.85 s | PASS |
| Prompt hits | 562 | 562 | 562 | PASS |
| Prompt misses | 67 | 67 | 67 | PASS |
| Prompt eligible | 629 | 629 | 629 | PASS |
| Prompt not recorded | 29 | 29 | not separately displayed | PASS DB/API |
| Prompt call hit rate | 0.893482 | 0.8934817170111288 | 89.3% | PASS |
| Prompt cache-read tokens | 913,280 | 913,280 | not separately displayed | PASS DB/API |
| Prompt token denominator | 1,689,294 | 1,689,294 | not separately displayed | PASS DB/API |
| Prompt token cache ratio | 0.540628 | 0.5406282151005094 | 54.1% | PASS |
| Embedding cache input hit rate | null/N/A | null | — | PASS (`NULL != 0`) |

DB values were obtained by a read-only SQL query. The raw API artifact was obtained
from the running local backend using an existing encrypted tenant API key; neither
the key nor an authorization header was written to evidence. The API response echoes
the exact fixed range and model ID.

The Dashboard screenshot uses `2026-08-31` through `2026-09-07` in Asia/Shanghai.
The UI necessarily requests `[2026-08-30T16:00:00Z, 2026-09-07T16:00:00Z)` because
its control is date-only. A separate read-only DB query proves that, for this model,
both ranges currently select the same 658 rows and identical aggregates. This is a
point-in-time row-set equivalence and must not be assumed after later writes.

## Verdict

**PASS**. Actual runtime DB values equal the actual authenticated Analytics API
values, and the user-supplied runtime Dashboard screenshot shows the corresponding
frontend-formatted values. The UI's inability to enter arbitrary UTC timestamps is
retained as an explicit limitation rather than hidden.
