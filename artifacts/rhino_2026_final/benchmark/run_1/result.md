# Benchmark v1.1 Final Result

Execution mode: STRICT  
Comparable to published baseline: YES  
Commit: `cd21908a652d1330499986e191fbd704b7d9c13b`  
Evaluation run: `eca1f236-0047-4fe3-aa11-39897e5d321b`  
Generated (UTC): `2026-09-07T08:46:04Z`  
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
| BLEU-1 | 0.161878 |
| BLEU-2 | 0.135830 |
| BLEU-4 | 0.098043 |
| ROUGE-1 | 0.282773 |
| ROUGE-2 | 0.154857 |
| ROUGE-L | 0.277034 |

## Runtime / Usage

- Worker limit: 27
- Cache mode: off
- Model calls: 46
- Average model latency: 1999.70 ms

Retrieval metrics are generally more stable. BLEU/ROUGE may vary slightly because hosted model behavior is not bit-for-bit deterministic.
