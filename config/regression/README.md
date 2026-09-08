# Regression CI 配置

这里是 Benchmark v1.1 Regression Gate 的输入数据与阈值策略。它们只被
`cmd/regression`（comparator）与 `cmd/regression-benchmark`（current generation）
消费，不参与 Benchmark 执行。

## 文件

- `baseline.json` — 冻结的 baseline。来自 Final strict Benchmark v1.1 Cache OFF Run 1
  （见下方 Baseline Provenance），保留 `quality`（12 metrics）+ `config`
  （frozen contract：dataset / retrieval / generation / model / worker_limit）+ 标识符，
  剥离 `model_facts` / `run_wall_clock_duration_ms` 等 operational 数据。
- `current.json` — committed deterministic healthy fixture，是 Final Run 1 的同一
  comparator-schema 投影，用于证明 no-regression 路径 PASS；它不是新的正式
  baseline，也不是新的 Evaluation run。真实 Benchmark 运行后用
  `cmd/regression-benchmark` 产出的 JSON 覆盖它。
- `policy.json` — 阈值策略。`default_allowed_drop` 是默认允许下降的绝对幅度，
  `allowed_drop` 可按 metric 覆盖。

## Baseline Provenance

| 字段 | 值 |
|---|---|
| task_id | `evaluation_10000_1788770724740_d35f5c12_benchmarkv1` |
| evaluation_run_id | `eca1f236-0047-4fe3-aa11-39897e5d321b` |
| benchmark version | `v1.1` |
| 来源 | `artifacts/rhino_2026_final/benchmark/run_1/result.json` + `metadata.json` |
| 选择规则 | first complete strict success（不是择优） |
| 冻结 | 是（quality metrics + benchmark contract） |

Run 1 是三个 Final strict runs 中预声明规则选出的首个完整成功 run，不是按指标
择优。`baseline.json` 从该 run 精确投影 comparator 所需的 run/config/quality/
reproducibility 字段，不包含 usage、cost、latency 等 operational 数据。

## Compatibility Check

Comparator 在比较 quality 前，先校验 baseline 与 current 的 benchmark contract
一致（`internal/regression/compat.go`）。检查的字段包括：

`benchmark_version`、`dataset.dataset_id / dataset_semantic_sha256 / corpus_count /
question_count`、`retrieval.vector_threshold / keyword_threshold / embedding_top_k /
rerank_top_k / rerank_threshold / retrieve_driver`、`models.embedding.name / provider /
dimension`、`models.chat.name / provider`、`models.rerank.name / provider`、
`execution.worker_limit`。

任何字段不一致 → `Compatibility: FAIL`（列出全部 mismatch），exit `1`。这防止把
「不同模型 / 不同 dataset / 不同检索 contract」误判为 quality regression。

## 阈值语义

所有 12 个 quality metrics 都是 higher-is-better。对每个 metric：

```
delta     = current - baseline
threshold = -allowed_drop        # 允许的最大绝对下降幅度（取负号后是 delta 下限）
PASS      = delta >= threshold   # 即 current >= baseline - allowed_drop
```

阈值统一使用**绝对下降**（不是百分比下降）。Retrieval 六项、BLEU-4 和
ROUGE-2 为 `0.020`；BLEU-1 为 `0.030`，BLEU-2 为 `0.025`，ROUGE-1 和
ROUGE-L 为 `0.035`。Generation 阈值来自 Final strict x3 的 empirical worst
downward delta 加小幅显式余量，不使用 3σ。任一 metric 下降超过自己的
threshold → 整体 FAIL（禁止加权抵消）。

## 运行

Comparator（比较 baseline vs current）：

```bash
go run ./cmd/regression \
  --baseline config/regression/baseline.json \
  --current  config/regression/current.json \
  --policy   config/regression/policy.json
```

生成 current（真实 Benchmark，需要 DB + 模型 provider 环境）：

```bash
go run ./cmd/regression-benchmark --output artifacts/regression/current.json
```

exit code：`0` = PASS，`1` = regression（或 metric 缺失 / 非有限数 /
contract mismatch），`2` = 执行错误（文件不可读 / JSON 损坏 / Benchmark 失败）。
质量退化、contract mismatch 或 Benchmark 执行失败绝不会返回 `0`。
