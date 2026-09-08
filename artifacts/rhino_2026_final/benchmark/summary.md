# Final Benchmark Summary

## Identity

- Commit: `cd21908a652d1330499986e191fbd704b7d9c13b`
- Dataset: `benchmark_v1` / `v1.1`
- Semantic SHA-256: `56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d`
- Counts: corpus 32 / questions 15 / qrels 15 / answers 15
- Models: `deepseek-v4-pro` (chat/summary), `text-embedding-v4`, `qwen3-rerank`
- Worker limit: 27
- Embedding cache: OFF
- Positioning: small, deterministic, controlled acceptance benchmark

## Strict Preflight

**PASS.** The frozen dataset, model identities, runtime profile, writable output, and
database/backend checks passed. Preflight-only mode made no Evaluation or provider
call.

Custom preflight also **PASSed**, but only exercised the explicitly non-comparable
path; no Custom benchmark was executed.

Evidence: [artifacts/rhino_2026_final/preflight.md](../preflight.md)

## Three Strict Runs

Each run completed 15/15 items with metric state `complete`.

| Metric | Run 1 | Run 2 | Run 3 |
|---|---:|---:|---:|
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

## Variance

The six retrieval metrics have zero range at the published nine-decimal precision.
Raw JSON records a `2.7755575615628914e-17` Precision range from floating-point
representation; Recall, NDCG@3, NDCG@10, MRR, and MAP are exactly zero-range.

| Generation metric | Range |
|---|---:|
| BLEU-1 | 0.031626279 |
| BLEU-2 | 0.026970171 |
| BLEU-4 | 0.018665013 |
| ROUGE-1 | 0.050218768 |
| ROUGE-2 | 0.033696411 |
| ROUGE-L | 0.041220089 |

Retrieval is stable; lexical generation metrics vary under the same frozen hosted
generation configuration. This observation does not prove provider randomness as
the sole cause.

## Published Result

**Run 1** is published because it is the first complete successful Final Strict run
under the predeclared selection rule. Runs 2 and 3 are variance evidence; no best-run
selection was performed.

## Evidence

- [artifacts/rhino_2026_final/benchmark/run_1/result.json](run_1/result.json), [result.md](run_1/result.md), [metadata.json](run_1/metadata.json)
- [artifacts/rhino_2026_final/benchmark/run_2/result.json](run_2/result.json), [result.md](run_2/result.md), [metadata.json](run_2/metadata.json)
- [artifacts/rhino_2026_final/benchmark/run_3/result.json](run_3/result.json), [result.md](run_3/result.md), [metadata.json](run_3/metadata.json)
- [artifacts/rhino_2026_final/benchmark/variance_summary.json](variance_summary.json)
- [artifacts/rhino_2026_final/benchmark/variance_summary.md](variance_summary.md)
