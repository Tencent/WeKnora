# 1. Final Candidate

- Commit: `cd21908a652d1330499986e191fbd704b7d9c13b` (`fix(evaluation): harden final benchmark modes`)
- Branch: `feat/topic3-evaluation`
- Dataset: `benchmark_v1` / `v1.1`
- Semantic SHA-256: `56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d`
- Counts: corpus 32, questions 15, qrels 15, answers 15
- Positioning: small, deterministic, controlled acceptance benchmark
- Production-code changes in this phase: none

# 2. Preflight

Strict preflight PASS and Custom preflight PASS. Both were preflight-only: no Evaluation and no provider call. Strict confirmed the frozen dataset/model/runtime profile, worker limit 27, cache OFF, and comparable output. Custom confirmed the non-comparable path without running a Custom benchmark.

# 3. Final Benchmark Runs

All three runs are complete Strict runs with 15/15 items and all 12 metrics present.

| Metric | Run 1 | Run 2 | Run 3 |
| --- | ---: | ---: | ---: |
| Precision | 0.139003867 | 0.139003867 | 0.139003867 |
| Recall | 1.000000000 | 1.000000000 | 1.000000000 |
| NDCG@3 | 1.000000000 | 1.000000000 | 1.000000000 |
| NDCG@10 | 1.000000000 | 1.000000000 | 1.000000000 |
| MRR | 1.000000000 | 1.000000000 | 1.000000000 |
| MAP | 1.000000000 | 1.000000000 | 1.000000000 |
| BLEU-1 | 0.161878128 | 0.168613543 | 0.136987264 |
| BLEU-2 | 0.135830178 | 0.143252003 | 0.116281832 |
| BLEU-4 | 0.098042695 | 0.101756377 | 0.083091364 |
| ROUGE-1 | 0.282772802 | 0.306109408 | 0.255890639 |
| ROUGE-2 | 0.154857479 | 0.172751379 | 0.139054968 |
| ROUGE-L | 0.277034096 | 0.291294593 | 0.250074504 |

Run IDs: `eca1f236-0047-4fe3-aa11-39897e5d321b`, `b749008d-cd0f-4bb4-a7f8-dfefbc079d75`, `b34144cd-055e-4abb-8048-19ee10c5bfe9`.

# 4. Variance Analysis

All six retrieval metric ranges are exactly zero. Generation metric ranges are: BLEU-1 0.031626279, BLEU-2 0.026970171, BLEU-4 0.018665013, ROUGE-1 0.050218768, ROUGE-2 0.033696411, ROUGE-L 0.041220089.

The evidence supports this bounded statement: retrieval is stable while lexical answer-quality metrics exhibit run-to-run variance under the same frozen hosted-generation configuration. It does not prove provider randomness as the sole cause.

# 5. Published Final Result

Run 1 is published because it is the first complete successful Final Strict run, per the predeclared selection rule. Runs 2 and 3 are variance evidence only; no best-run selection was performed.

# 6. Answer Audit

INCOMPLETE for final-answer correctness. `answers.json` enumerates all 15 real questions, reference answers, and qrel documents, but the aggregate published artifact/persistence layer does not retain per-question generated answers or retrieved chunks. Those fields remain null/empty and all provisional verdicts are `needs_human_review`. No “15/15 correct” claim is made.

# 7. Regression

- Real current result: PASS, exit 0; all 12 monitored metrics within the unchanged policy.
- Forced Recall case: FAIL, exit 1; Recall baseline 1.0, current 0.5, delta -0.5, allowed drop 0.02. Only Recall failed.
- Baseline and thresholds were not changed.

# 8. Embedding Cache

Same commit/model/dataset SHA/32 texts and production pooled-batch path; only cache state changed.

| Phase | Logical inputs | Hits | Misses | Provider inputs | Provider requests | Elapsed |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| OFF | 32 | 0 | 0 | 32 | 7 | 670 ms |
| COLD | 32 | 0 | 32 | 32 | 7 | 584 ms |
| WARM | 32 | 32 | 0 | 0 | 0 | 10 ms |

WARM eliminated provider inputs/requests for this controlled set. Latency is auxiliary, not an SLA. One invalid setup probe exceeded the provider's per-batch limit and is retained/excluded in the evidence.

# 9. Wiki Prompt Cache

Two Final-Candidate isolated arms completed with identical corpus/models/pipeline and different cohort markers. Each produced 4/4 completed files and 50 published pages.

| Metric | A | B |
| --- | ---: | ---: |
| Eligible wiki calls | 59 | 59 |
| Hit / miss | 57 / 2 | 57 / 2 |
| Call hit rate | 0.966102 | 0.966102 |
| Cache-read tokens | 81,408 | 94,976 |
| Prompt token cache ratio | 0.475184 | 0.540435 |
| Provider timeout rows | 1 | 10 |
| `wiki_summary` | 4/4 hit, 2,048 read | 4/4 hit, 2,048 read |

Supported conclusion: the Wiki summary layer demonstrates reproducible prompt-cache reuse. No improvement claim is made for `wiki_page_modify` or all Wiki stages. Full BA crossover was not run after the B provider-degraded window reached limits, so the requested AB/BA item is INCOMPLETE.

# 10. Model Usage Reconciliation

DB snapshot for tenant 10000/window `2026-08-31T13:00:00Z` to `2026-09-07T11:00:00Z` contains 1,011 rows: 658 chat, 217 embedding, 136 rerank. Nullable token fields remain null, not zero. Derived DB values: prompt call hit rate 0.893482, prompt token cache ratio 0.540628, embedding input hit rate 0.5.

Backend repository and frontend contract/formatting tests PASS. A fresh authenticated API capture was attempted twice in the final continuation, but the execution approval review timed out before the command ran; no raw API response or authenticated dashboard runtime capture exists. Therefore DB aggregation is PASS, while full DB/API/Dashboard runtime reconciliation is INCOMPLETE.

# 11. Acceptance Matrix

| Required item | Status | Evidence |
| --- | --- | --- |
| Frozen candidate/dataset | PASS | exact commit/SHA/counts |
| Strict + Custom preflight | PASS | preflight-only checks |
| Three Strict benchmarks | PASS | 3 complete independent runs |
| Variance analysis | PASS | retrieval zero-range; generation ranges recorded |
| Published result rule | PASS | first complete success, Run 1 |
| Answer-level correctness | INCOMPLETE | generated answers not persisted; human review required |
| Regression real current | PASS | comparator exit 0 |
| Forced Recall rejection | PASS | comparator exit 1, Recall only |
| Embedding OFF/COLD/WARM | PASS | 32 controlled inputs; warm 0 provider |
| Wiki summary reuse | PASS | two arms, 4/4 hits each |
| Full Wiki AB/BA crossover | INCOMPLETE | AB complete; BA not run after provider limits |
| Model Usage DB aggregate | PASS | 1,011-row snapshot and nullable semantics |
| DB/API/Dashboard runtime reconciliation | INCOMPLETE | API/UI runtime evidence absent |

Overall: REQUIRED ACCEPTANCE INCOMPLETE. No correctness blocker was found in the completed evidence; remaining gaps are evidence-capture gaps and one unrun crossover.

# 12. Artifact Inventory

- `preflight.md`, `validation.md`, `final_report.md`
- `benchmark/run_{1,2,3}/{result.json,result.md,metadata.json}`
- `benchmark/{variance_summary.json,variance_summary.md}`
- `regression/pass/{current.json,regression-report.json,command.txt}`
- `regression/forced_fail/{current_forced_fail.json,regression-report.json,command.txt}`
- `embedding_cache/{off.json,cold.json,warm.json,model_usage_rows.json,comparison.md}`
- `wiki_prompt_cache/ab/{a1.json,b1.json,comparison.json,comparison.md}`
- `wiki_prompt_cache/ba/README.md`
- `model_usage/{db_snapshot.json,reconciliation.md,api_capture_attempts.md}`
- `answer_audit/{answers.json,answer_audit.md}`

# 13. Remaining Risks

1. Generated answers/retrieved chunks were not persisted, so answer correctness cannot be audited retroactively.
2. Wiki BA crossover is missing; B also recorded 10 real provider timeout rows, so latency is degraded/noisy.
3. No raw authenticated Analytics API response or dashboard runtime capture is available; source/contract tests do not replace runtime reconciliation.
4. Hosted generation lexical ranges exceed 0.02 for several metrics. This is evidence for review, not authorization to loosen policy.

# 14. Git Status

`git status --short`:

```text
?? artifacts/rhino_2026_final/
```

`git rev-parse HEAD`:

```text
cd21908a652d1330499986e191fbd704b7d9c13b
```

No commit and no push were performed in this acceptance phase.
