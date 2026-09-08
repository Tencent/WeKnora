# WeKnora Topic 3 Final Acceptance Summary

## 1. Final Candidate

- Commit: `cd21908a652d1330499986e191fbd704b7d9c13b`
- Branch: `feat/topic3-evaluation`
- Dataset: `benchmark_v1` / `v1.1`
- Semantic SHA-256: `56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d`
- Counts: corpus 32 / questions 15 / qrels 15 / answers 15
- Positioning: small, deterministic, controlled acceptance benchmark

## 2. Acceptance Overview

| Area | Status | Key Result | Evidence |
|---|---|---|---|
| Final Benchmark | PASS | Three complete Strict runs, each 15/15 with 12 metrics | [benchmark/summary.md](benchmark/summary.md) |
| Variance | PASS WITH LIMITATIONS | Retrieval stable at reporting precision; lexical generation metrics vary | [benchmark/summary.md](benchmark/summary.md) |
| Regression Comparator | PASS WITH LIMITATIONS | Final Run 1 baseline and calibrated policy frozen; normal exit 0; forced Recall rejection exit 1; real CI degradation run pending | [regression/summary.md](regression/summary.md) |
| Embedding Cache | PASS | Warm phase served 32/32 inputs with zero provider work | [embedding_cache/summary.md](embedding_cache/summary.md) |
| Wiki Prompt Cache | PASS WITH LIMITATIONS | Historical summary-layer improvement plus reproducible Final Candidate reuse | [wiki_prompt_cache/summary.md](wiki_prompt_cache/summary.md) |
| Model Usage | PASS | Runtime DB, authenticated API, and Dashboard values reconcile | [model_usage/summary.md](model_usage/summary.md) |
| Answer Audit | HUMAN REVIEW | Per-question generated answers and retrieved chunks were not persisted | [answer_audit/answer_audit.md](answer_audit/answer_audit.md) |
| Optional Parser | OPTIONAL | No optional parser evidence is required for this acceptance package | — |

## 3. Benchmark

All three Strict runs completed 15/15 items and all 12 metrics. The six retrieval
metrics are stable at the published nine-decimal precision: Precision `0.139004`,
Recall `1.0`, NDCG@3 `1.0`, NDCG@10 `1.0`, MRR `1.0`, and MAP `1.0`. The raw
Precision range is `2.78e-17`, an IEEE-754 representation tail; all other retrieval
ranges are exactly zero.

Generation lexical ranges are BLEU-1 `0.031626`, BLEU-2 `0.026970`, BLEU-4
`0.018665`, ROUGE-1 `0.050219`, ROUGE-2 `0.033696`, and ROUGE-L `0.041220`.

Run 1 is the published result because it was the first complete successful Strict
run under the predeclared selection rule, not because it was the best run.

Evidence: [artifacts/rhino_2026_final/benchmark/summary.md](benchmark/summary.md)

## 4. Regression

- Normal comparator: **PASS**, exit `0`.
- Forced Recall case: **FAIL AS EXPECTED**, exit `1`; Recall `1.0 -> 0.5`, delta
  `-0.5`, allowed drop `0.02`, and Recall was the only failed metric.
- Final Regression baseline: Final strict Run 1,
  `eca1f236-0047-4fe3-aa11-39897e5d321b`, selected as the first complete strict
  success rather than by metric performance.
- Frozen thresholds: all six Retrieval metrics `0.020`; BLEU-1 `0.030`, BLEU-2
  `0.025`, BLEU-4 `0.020`, ROUGE-1 `0.035`, ROUGE-2 `0.020`, and ROUGE-L `0.035`.
- Real Recall-degrading GitHub CI evidence: **PENDING**.

Evidence: [artifacts/rhino_2026_final/regression/summary.md](regression/summary.md)

## 5. Embedding Cache

| Phase | Logical inputs | Hits | Misses | Provider inputs | Provider requests |
|---|---:|---:|---:|---:|---:|
| OFF | 32 | 0 | 0 | 32 | 7 |
| COLD | 32 | 0 | 32 | 32 | 7 |
| WARM | 32 | 32 | 0 | 0 | 0 |

Warm cache eliminated provider embedding work for the controlled 32-input set.

Evidence: [artifacts/rhino_2026_final/embedding_cache/summary.md](embedding_cache/summary.md)

## 6. Wiki Prompt Cache

Historical before/after evidence is **VALID WITH LIMITATIONS**. At `wiki_summary`,
BEFORE recorded 0/8 hits, 0% call-hit rate, and 0% token-cache ratio; AFTER recorded
7/8 hits, 87.5%, and 26.6786%.

In the Final Candidate A/B, both arms recorded `wiki_summary` 4/4 hits and an overall
eligible hit rate of 57/59 (`96.6102%`). The historical experiment supports the
optimization direction; the Final Candidate A/B supports reproducibility of the
optimized reuse behavior. It does not prove that all Wiki stages improved, and a
full AB/BA crossover was not completed.

Evidence: [artifacts/rhino_2026_final/wiki_prompt_cache/summary.md](wiki_prompt_cache/summary.md)

## 7. Model Usage

For `deepseek-v4-pro`, the runtime reconciliation reports 658 calls, 1,689,294 input
tokens, 1,409,722 output tokens, 3,099,016 total tokens, and 35.85 s average latency.
Prompt cache recorded 562 hits and 67 misses: 89.3% call-hit rate and 54.1% token-cache
ratio. DB = authenticated Analytics API = Dashboard under the same effective data
window.

Evidence: [artifacts/rhino_2026_final/model_usage/summary.md](model_usage/summary.md)

## 8. Answer Audit

Status: **HUMAN REVIEW**. Aggregate `BenchmarkResult` persistence did not retain
per-question generated answers or retrieved chunks, so answer-level correctness
cannot be reconstructed. No claim of “15/15 correct” is made.

Evidence: [artifacts/rhino_2026_final/answer_audit/answer_audit.md](answer_audit/answer_audit.md)

## 9. Known Limitations

- Answer-level semantic audit is unavailable from aggregate persistence.
- Wiki historical before/after has order, warm-state, and sample-size limitations.
- Provider timeouts were observed in the Wiki B arm.
- Generation lexical metrics exhibit run-to-run variance.
- Real Recall-degrading CI evidence remains pending.

## 10. Artifact Index

- [artifacts/rhino_2026_final/benchmark/summary.md](benchmark/summary.md)
- [artifacts/rhino_2026_final/regression/summary.md](regression/summary.md)
- [artifacts/rhino_2026_final/embedding_cache/summary.md](embedding_cache/summary.md)
- [artifacts/rhino_2026_final/wiki_prompt_cache/summary.md](wiki_prompt_cache/summary.md)
- [artifacts/rhino_2026_final/model_usage/summary.md](model_usage/summary.md)
- [artifacts/rhino_2026_final/answer_audit/answer_audit.md](answer_audit/answer_audit.md)
