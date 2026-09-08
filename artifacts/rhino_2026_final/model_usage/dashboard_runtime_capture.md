# Model Usage Dashboard Runtime Capture

## Evidence identity

- Page: `/platform/settings?section=models`
- Runtime screenshot: supplied inline by the user on 2026-09-07; the screenshot
  itself is retained in the conversation, not duplicated as a repository image.
- Model: `deepseek-v4-pro`
- UI date range: `2026-08-31` through `2026-09-07`
- Interval: Day (`按天`)
- Browser/UI timezone: Asia/Shanghai (UTC+08:00)

The date-only UI converts this selection to the half-open API range
`2026-08-30T16:00:00Z` through `2026-09-07T16:00:00Z`. That is wider than the
fixed DB/API capture range (`2026-08-31T13:00:00Z` through
`2026-09-07T11:00:00Z`). A read-only DB comparison confirmed that the wider UI
range contains no additional rows for this tenant/model, so both ranges select
the same 658-row set and therefore have identical aggregate values.

## Values visibly confirmed in the screenshot

| Dashboard field | Displayed value | DB/API underlying value | Match |
|---|---:|---:|---|
| Calls | 658 | 658 | PASS |
| Total Tokens | 309.9万 | 3,099,016 | PASS (formatted) |
| Input Tokens | 168.9万 | 1,689,294 | PASS (formatted) |
| Output Tokens | 141万 | 1,409,722 | PASS (formatted) |
| Input observation coverage | 629 / 658 | 629 / 658 | PASS |
| Average latency | 35.85 s | 35,849.83434650456 ms | PASS (formatted) |
| Prompt Cache Call Hit Rate | 89.3% | 562 / 629 = 0.8934817170111288 | PASS (formatted) |
| Prompt Token Cache Ratio | 54.1% | 913,280 / 1,689,294 = 0.5406282151005094 | PASS (formatted) |
| Prompt cache detail | hit 562 / miss 67 / eligible 629 | 562 / 67 / 629 | PASS |
| Embedding input hit rate | — | null under chat-model filter | PASS (`NULL != 0`) |

The screenshot visibly includes the model, date range, interval, summary cards,
and both Prompt Cache metrics. Compact-number and percentage rounding follow the
current frontend formatters; no rounded display value is treated as a raw-value
mismatch.

## Verdict

**PASS**, with a documented date-control limitation: the Dashboard cannot express
the original arbitrary UTC timestamps, but its generated range currently resolves
to the exact same model-usage row set. This establishes runtime value parity, not
general equivalence of those two time ranges for future data.
