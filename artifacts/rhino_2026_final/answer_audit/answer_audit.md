# Answer-Level Human Correctness Audit

- **Commit**: `cd21908a652d1330499986e191fbd704b7d9c13b`

## Status: HUMAN REVIEW

Reason: generated answers and retrieved chunks were not persisted in aggregate
`BenchmarkResult`, so the answer-level audit is not reconstructible. No data was
fabricated.

The Final Candidate benchmark artifacts store **aggregate** `BenchmarkResult` only.
Neither the artifact files nor the persistence layer retain per-question generated
answers or retrieved evidence:

- `artifacts/rhino_2026_final/benchmark/run_*/result.json` top-level keys are
  `metrics`, `usage`, `latency`, `reproducibility`, `models`, `runtime` — aggregate
  summaries, no per-question answer/evidence.
- The only evaluation table, `evaluation_runs`, exposes 30 aggregate columns
  (precision, recall, ndcg_3/10, mrr, map, bleu_1/2/4, rouge_1/2/l, status, counts)
  and **no** question/answer/evidence column.

Therefore: **answer-level human correctness audit cannot be reconstructed from the
aggregate BenchmarkResult alone.** No per-question answer or retrieved evidence is
being synthesized to fill this gap.

`answers.json` still enumerates all 15 frozen questions, reference answers, and qrel
documents from `benchmark_v1`. The unavailable generated-answer and retrieved-output
fields are explicitly `null`/empty and every provisional verdict is
`needs_human_review`.

## What this implies

- No AI verdict of "15/15 correct" is asserted. Any correctness claim would require
  per-question answers that are not persisted.
- Final human confirmation is required and remains **the user's** to perform.
- If a human audit is required, it must be run against a fresh benchmark execution
  that explicitly persists per-question answers/evidence — out of scope for the
  current frozen Final Candidate.
