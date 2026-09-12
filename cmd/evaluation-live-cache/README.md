# Controlled live cache experiment

This command runs a fixed synthetic experiment through the production embedding/chat factories, model-call ledger, SQLite cache repositories and Wiki page-update orchestration. It creates one fresh artifact directory and refuses to reuse any existing directory. It does not load application databases or saved credentials.

The default command is a zero-network dry-run:

```sh
go run ./cmd/evaluation-live-cache --out /artifacts/x08-dry-run
```

The plan fixes the Beijing DashScope endpoint, `qwen3.7-flash`, `qwen3.7-text-embedding`, prices, synthetic corpus, request order and generation parameters. Review `plan.json` and `comparison.md` before authorizing execution. The plan has at most 16 chat requests and 4 embedding requests. Each chat output is capped at 256 tokens, thinking is disabled, and temperature is 0.3. The two Wiki arms contain the same text and differ only in shared-context placement. `CacheRetentionNone` suppresses explicit cache markers in both arms; provider implicit caching remains active. The shared-context estimate uses `cl100k_base`, not the Qwen tokenizer, and is not proof of provider eligibility or a hit.

Execution requires all three explicit confirmations: `--execute`, the exact reviewed `--confirm-plan-sha256`, and `--approve-cny`. The approved amount must cover the complete plan and cannot exceed CNY 3.00. The runtime ceiling is the smaller of the approved amount and the plan reservation ceiling. A budget reservation is committed before each provider request and is never refunded during the run. Each planned step permits at most one external attempt; transport failures, unknown usage and rejected requests stop the experiment.

After spending approval, the authorized operator provides the key through `X08_DASHSCOPE_API_KEY` or `--key-stdin`. The credential is read only after execution confirmations pass, and only the fixed-destination gateway receives it. Do not put the key in command-line arguments, the plan or artifact files.

Separate saved credentials can be supplied with `--keys-json-stdin`, using an object with `chat` and `embedding` string fields. Both are validated after approval and before any outbound request. Supply the object through a process pipe; never write it to a file or a command-line argument. The experiment fixes embedding dimensions at 1024. Its isolated database leaves existing indexes and model settings intact.

An execution command uses a fresh output directory:

```sh
go run ./cmd/evaluation-live-cache --execute \
  --confirm-plan-sha256 REVIEWED_PLAN_SHA256 \
  --approve-cny 1.00 --key-stdin --out /artifacts/x08-approved-run
```

The embedding comparison uses four fixed inputs in two batches, then repeats both batches against the same isolated persistent cache. Successful cache behavior produces two cold physical requests and zero warm physical requests, with identical vector hashes. The Wiki comparison uses four page pairs, two message orders and two rounds. First and repeated observations are labels for request order; the first provider observation may already hit a shared provider cache. An all-miss result, missing cache telemetry or no observed ordering advantage produces an inconclusive result and a nonzero exit status.

Artifacts include the frozen `plan.json`, isolated `x08.sqlite`, incremental `steps.json`, numeric allowlisted provider usage in `provider-usage.json`, `result.json` and `comparison.md`. Prices and complete usage come from the production ledger; unknown values remain null. Reservations, frozen price identities and per-step request hashes remain available for audit. The small output checks cover required facts and output framing and require human review for full semantic equivalence.

`python3 scripts/evaluation-live-cache-openrouter.py --run RUN_DIR --catalog-dir FROZEN_CATALOG_DIR --out NEW_REPORT.json` adds an offline OpenRouter price comparison while preserving the CNY ledger. It validates catalog digests and model identities, applies context tiers and separates cache reads from ordinary input. The report preserves unknown embedding quotes and cache-write adjustments. A partial price estimate is not a supplier bill. Verify the calculator with `python3 scripts/test_evaluation_live_cache_openrouter.py`.

Offline verification runs the same production factories and Wiki orchestration against an in-process fixture transport. Its results carry `mode: offline-fixture` and demonstrate program contracts only:

```sh
go test ./internal/evaluation/livecache ./cmd/evaluation-live-cache
go test -race ./internal/evaluation/livecache
```

The Beijing list-price basis is [Flash model information](https://help.aliyun.com/zh/model-studio/qwen3-7-flash), [text embedding model information](https://help.aliyun.com/zh/model-studio/qwen3-7-text-embedding), and [context-cache behavior](https://help.aliyun.com/zh/model-studio/context-cache). Request bodies use UTF-8 byte lengths plus a framing allowance as a conservative token envelope. Provider-reported usage is authoritative; any reported amount outside its reservation stops further requests.
