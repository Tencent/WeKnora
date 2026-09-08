# Benchmark v1.1 Final Result

Execution mode: STRICT  
Comparable to published baseline: YES  
Commit: `cd21908a652d1330499986e191fbd704b7d9c13b`  
Evaluation run: `b34144cd-055e-4abb-8048-19ee10c5bfe9`  
Generated (UTC): `2026-09-07T08:47:48Z`  
Dataset SHA: `56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d`

Models: embedding `text-embedding-v4` (generic), chat `deepseek-v4-pro` (generic), summary `deepseek-v4-pro` (generic), rerank `qwen3-rerank` (aliyun)

## Quality

| Metric | Value |
| --- | ---: |
| Precision | 0.139004 |
| Recall | 1.000000 |
| NDCG@3 | 1.000000 |
| NDCG@10 | 1.000000 |
| MRR | 1.000000 |
| MAP | 1.000000 |
| BLEU-1 | 0.136987 |
| BLEU-2 | 0.116282 |
| BLEU-4 | 0.083091 |
| ROUGE-1 | 0.255891 |
| ROUGE-2 | 0.139055 |
| ROUGE-L | 0.250075 |

## Runtime / Usage

- Worker limit: 27
- Cache mode: off
- Model calls: 46
- Average model latency: 1567.20 ms

Retrieval metrics are generally more stable. BLEU/ROUGE may vary slightly because hosted model behavior is not bit-for-bit deterministic.
