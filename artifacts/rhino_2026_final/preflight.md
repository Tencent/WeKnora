# Final Candidate Preflight

- Commit: `cd21908a652d1330499986e191fbd704b7d9c13b`
- Branch: `feat/topic3-evaluation`
- Runtime: backend `127.0.0.1:8080`; PostgreSQL `127.0.0.1:5432`; Redis `127.0.0.1:6379`; DocReader `127.0.0.1:50051`
- Host override: ignored `.env.local`; no business-code change and no credential recorded here.

## Strict

PASS. Preflight-only mode made no Evaluation and no provider call.

- benchmark: `benchmark_v1`, version `v1.1`
- semantic SHA-256: `56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d`
- counts: corpus 32, questions 15, qrels 15, answers 15
- embedding: `text-embedding-v4`, dimension 1024
- chat/summary: `deepseek-v4-pro`
- rerank: `qwen3-rerank`
- worker limit: 27
- embedding cache: OFF
- output: comparable
- database/backend/output-writable checks: PASS

## Custom

PASS. Preflight-only mode used the same frozen dataset/runtime profile, accepted the configured model identities, marked output non-comparable, and made no Evaluation/provider call. No Custom benchmark was executed.
