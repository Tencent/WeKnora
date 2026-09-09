# 评测回归门禁、模型调用可观测性与 Embedding 缓存

[English](./README.md) | 简体中文

本目录记录 WeKnora 的评测回归门禁、租户级模型调用可观测性和
Embedding 持久化缓存实现。目标不是增加一个孤立的统计页面，而是建立从
“调用记录、成本分析、缓存优化”到“质量退化阻断、真实实验复验”的完整闭环。

## 功能概览

```text
普通问答 / 评测 / Wiki / 后台任务
              │
              ▼
       租户级模型调用观察器
              │  异步批量写入
              ▼
 evaluation_model_calls ──► 用量 API ──► 模型设置页可视化

Embedding 输入 ──► 进程内缓存 ──► 租户级持久缓存 ──► Provider
                                      │
                                      └─► 命中、去重、避免计算统计

生产检索代码 ──► 指标计算 ──► 绝对门槛 + 前后差值 ──► CI 通过 / 阻断退化
```

系统提供以下能力：

- 持久化评测任务、运行配置、模型调用明细和用量快照。
- 聚合普通问答、评测、Wiki 与后台任务中的模型调用元数据。
- 按模型和调用用途展示调用次数、Token、缓存、估算费用和耗时。
- 单独汇总 Wiki 调用，并展示 Embedding 缓存命中与避免计算次数。
- 使用租户隔离的数据库缓存复用相同模型、相同文本的 Embedding 向量。
- 对 Precision、Recall、NDCG@10、MRR、ROUGE-L、耗时和费用执行绝对值与相对退化双门禁。
- 提供可执行的退化用例，证明 Recall 下降会真正使 CI 失败。
- 为每条样本保存检索排名、逐项指标和内容哈希，并导出带 SHA-256 校验的证据报告。
- 提供中、英、俄、韩四种界面文案，未知调用用途安全回退为可读文本。

## 需求与实现对应

| 目标 | 系统实现 | 验收证据 |
| --- | --- | --- |
| 评测结果可持久化、可复查 | 保存任务状态、运行配置、指标、模型用量和费用 | 评测 API、数据库迁移和存储测试 |
| 统计每次模型调用 | 全局观察器覆盖普通问答、评测、Wiki 和后台任务 | `GET /evaluation/model-usage` 与模型设置页 |
| 分析 Token、费用、耗时和缓存 | 按模型及用途聚合，并区分已定价和未定价调用 | 模型卡片、用途明细和 Wiki 专项汇总 |
| 降低重复 Embedding 计算 | 进程内缓存加租户级持久化缓存，支持 TTL | Ollama 冷/暖索引重建实验 |
| 防止质量回退 | 生产检索路径生成结果，同时检查最低分与相对基线的差值 | `make evaluation-gate` 和云端 CI |
| 保护租户数据和提示词 | 不接收 Prompt/响应正文；按租户查询和缓存 | 隐私设计、隔离测试和 30 天清理策略 |
| 证明结果并非事后反推 | 记录代码、数据集、配置、模型和逐样本指纹，导出确定性报告 | “导出证据”按钮与 `evidenceverify` 校验器 |

## 用户界面

进入 **设置 → 模型设置**。每张模型卡片会在存在数据时显示：

- 调用次数与 Token 总量；
- Provider 上报的缓存 Token 命中率；
- 按配置价格计算的费用，未配置价格时明确显示“成本未配置”；
- Embedding 向量缓存命中率和避免计算次数；
- 最近 24 小时、7 天、30 天或全部保留期的数据。

展开“明细”后，可查看 Wiki 专项汇总和 `chat`、`embedding`、
`rerank`、`wiki_page_modify` 等用途的调用占比、Token、缓存和费用。
接口加载失败只影响用量展示，不影响模型配置本身。

模型用量接口和返回字段见[评测功能 API](../docs/api/evaluation.md)。接口只返回当前租户的数据，
支持使用 RFC3339 格式的 `start_time`、`end_time` 限定时间范围。

在 **设置 → RAG 评测 → 评测历史** 中，成功运行可点击“导出证据”。新版本运行会包含：

- 实际代码提交号，以及数据集、完整配置和受控变量配置的 SHA-256；
- 每条样本的 QID/AID、检索顺序、匹配数据集 PID、得分和逐项指标；
- 问题、参考答案、模型回答及检索正文的 SHA-256，不包含这些正文；
- 原始模型调用的 Token、缓存、费用、耗时、用途和时间戳。

下载后可在仓库根目录执行完整性复核：

```bash
go run ./cmd/evidenceverify -report evaluation-evidence-任务ID.json
```

输出 `evaluation evidence checksum verified` 说明报告自服务器生成后未发生改动。
SHA-256 是完整性校验而非身份签名；结果可信度还依赖报告中的代码提交号、冻结数据集指纹、
配置指纹，以及同协议重新运行所得结果。旧运行因当时没有逐样本采集，会在导出报告中带有
`per_sample_evidence_unavailable`，不能冒充新版本的完整证据。

## 隐私、隔离与保留策略

全局可观测记录只包含：

- 租户、模型和调用用途；
- Prompt、Completion、缓存读写与未命中 Token；
- 估算费用及币种、模型耗时和成功状态；
- 可选的稳定前缀与完整请求 HMAC 指纹。

模型调用观察类型不接收 Prompt 或响应正文，Provider 错误文本也不会写入全局记录。
评测运行快照中的 Prompt、上下文模板和回退文本也只保存 SHA-256，不保存正文。
如果未配置独立的 HMAC 密钥，则不保存两类指纹；配置后使用 HMAC-SHA256 保护，
不应复用 JWT、数据库或模型 API 密钥。

模型调用与 Embedding 缓存用量按租户隔离。元数据默认保留 30 天，服务启动
10 分钟后执行首次清理，此后每 24 小时清理过期记录。可使用以下环境变量调整：

```dotenv
WEKNORA_MODEL_CALL_OBSERVABILITY_ENABLED=true
WEKNORA_MODEL_CALL_RETENTION_DAYS=30
WEKNORA_MODEL_CALL_FINGERPRINT_KEY=replace-with-a-random-secret
```

Embedding 向量持久缓存键同时受租户、模型和输入文本影响，并按 TTL 过滤过期项，
避免跨租户复用向量或把过期数据当作命中。

## 一键本地验收

在仓库根目录运行：

```bash
make evaluation-gate
```

该命令会：

1. 运行指标、生产检索路径、可观测性、缓存和三个门禁命令的测试；
2. 通过当前生产排序与 TopK 代码生成 `evaluation-pipeline-result.json`；
3. 将结果同时与 [`baseline.json`](./baseline.json) 的绝对门槛及冻结参考结果比较，生成 `evaluation-report.json`；
4. 使用固定前后数据生成 `cache-comparison.json`。

当前确定性流水线结果为：

| 指标 | 实测值 | 门槛 |
| --- | ---: | ---: |
| Precision | 0.6667 | ≥ 0.5 |
| Recall | 1.0000 | ≥ 0.5 |
| NDCG@10 | 0.6934 | ≥ 0.5 |
| MRR | 0.5000 | ≥ 0.5 |
| ROUGE-L | 约 1.0000 | ≥ 0.2 |
| 费用 | 0 USD | ≤ 5 USD |

候选结果由生产检索代码生成，不读取预先提交的“通过结果”文件。门禁使用
[`fixtures/regression_reference.json`](./fixtures/regression_reference.json) 作为明确冻结的上一版参考，
报告逐项展示 `reference`、`actual` 和带符号的 `delta`。质量指标相对参考最多下降
0.05，费用相对参考最多增加 0.5 USD；同时仍必须满足表中的绝对门槛，避免通过降低
参考值或放宽单一口径掩盖退化。

如需手动比较一次候选结果：

```bash
go run ./cmd/evalgate \
  -result candidate.json \
  -reference-result evaluation/fixtures/regression_reference.json \
  -baseline evaluation/baseline.json \
  -report evaluation-report.json
```

冻结参考只能由可复现且已接受的运行结果通过独立代码审查更新，不能为了让某次候选运行
通过而临时调整。

CI 还会运行 [`fixtures/regression_degraded.json`](./fixtures/regression_degraded.json)，
要求命令以退化状态退出，并核对失败路径必须是
`metric.retrieval_metrics.recall`。因此门禁既证明正常版本可以通过，也证明退化版本会被阻断。

已完成一次独立 GitHub Runner 复验：
[课题三云端验收记录](https://github.com/c4046929/WeKnora/actions/runs/33485034005)。
前端类型检查与生产构建、后端确定性门禁、Recall 退化识别和报告上传均成功；
验收分支明确跳过需要外部服务器密钥的 E2E job。

## 缓存对比工具

使用相同文档、模型和分块配置分别采集优化前后数据，然后运行：

```bash
go run ./cmd/cachebench \
  -strict \
  -before evaluation/fixtures/cache_before.json \
  -after evaluation/fixtures/cache_after.json \
  -report cache-comparison.json
```

[`fixtures/cache_before.json`](./fixtures/cache_before.json) 和
[`fixtures/cache_after.json`](./fixtures/cache_after.json) 是验证计算器与 CI 的确定性样例，
不是线上模型测量值。真实实验结果单独保存在 `evidence/`，避免把样例数据包装成真实收益。

`-strict` 会先校验冷、暖组的工作负载、模型和配置指纹完全一致，两组重复次数
相同且至少三次，Wiki 调用的模型、用途和完整请求指纹也必须一一成对；每个不同请求在冷、暖组各出现一次。冷组出现命中、
暖组零命中、调用失败或缺少供应商缓存数据都会直接使验收失败。报告同时输出
中位数、P95 延迟和每千 Prompt Token 归一化成本。不需要删除任何数据：使用从未执行过的固定
测试文档完成冷组，随后立即原样重放为暖组即可。

评测页面还提供“Wiki 缓存严格 A/B”入口。它使用生产 Wiki 页面更新提示结构，生成三个
互相隔离的冷请求并立即逐字重放暖请求，共调用所选模型六次，但不会读取或修改 Wiki 页面。
导出的单一 JSON 报告包含完整请求 HMAC、Provider 缓存计数、中位数、P95、代码版本、
工作负载/配置指纹、严格校验结果和报告 SHA-256，不包含 Prompt 或模型响应正文。可使用
同一完整性校验器复验：

```bash
go run ./cmd/evidenceverify -report wiki-cache-benchmark-<id>.json
```

受控 A/B 用于证明相同请求的缓存因果效果；模型用量页中真实 `wiki_*` 调用的时间区间统计
用于说明线上 Wiki 工作负载表现。两类证据不能互相冒充。

## 真实实验一：百炼 Wiki 缓存

2026-08-30 在本地 WeKnora 使用阿里云百炼 `qwen3.7-plus`，对两份固定
Markdown 文档执行连续 Wiki 操作。两份文档均解析和摘要成功，共生成并发布 13 个 Wiki 页面。

| 指标 | 冷调用 | 暖调用 |
| --- | ---: | ---: |
| 调用数 | 4 | 6 |
| Prompt Token | 10,620 | 16,411 |
| 缓存读取 Token | 0 | 6,528 |
| Token 缓存命中率 | 0% | 39.78% |
| 单次最高命中率 | 0% | 62.98% |
| 每千 Prompt Token 估算输入成本 | 0.002 CNY | 0.0013635 CNY |
| 平均耗时 | 6,058 ms | 9,025 ms |

按相同 Prompt Token 口径，估算输入成本下降约 **31.8%**；计入输出后，
每千 Prompt Token 的完整估算费用下降约 **2.9%**。缓存读取 Token 来自百炼响应并由
WeKnora 写入数据库，不是根据文本相似度推断。

本轮延迟没有改善：暖调用平均耗时增加 2,967 ms，并包含一次长尾调用。
因此这里只得出“缓存命中率和归一化输入成本改善”的结论，不宣称延迟改善。

完整证据：

- [中文实验报告](./evidence/wiki-cache-bailian-2026-08-30.md)
- [结构化结果](./evidence/wiki-cache-bailian-2026-08-30.json)
- [只读 SQL 复验脚本](./evidence/wiki-cache-bailian-2026-08-30.sql)

## 真实实验二：Ollama Embedding 持久化缓存

2026-08-30 使用本地 Ollama `nomic-embed-text:latest`（768 维）执行索引重建。
两轮使用相同模型、文件名、文件哈希和分块配置；冷缓存结束后重启后端，清空进程内缓存，
再创建新的知识库复验数据库持久缓存。

| 指标 | 冷缓存 | 重启后暖缓存 | 变化 |
| --- | ---: | ---: | ---: |
| Embedding 请求文本 | 17 | 17 | 0 |
| 缓存命中 | 0 | 4 | +4 |
| Provider 实际计算 | 17 | 13 | -4 |
| 避免计算 | 0 | 4 | +4 |
| 缓存命中率 | 0% | 23.53% | +23.53 个百分点 |

总 Provider 计算下降 **23.53%**。其中 4 个固定正文分块全部命中持久缓存，
该阶段 Provider 计算从 4 次降到 0 次，降幅 **100%**。剩余 13 个输入来自重新生成的
摘要和问题，文本不保证逐字一致，因此没有被误报为相同缓存键。

完整证据：

- [中文实验报告](./evidence/embedding-cache-ollama-2026-08-30.md)
- [结构化结果](./evidence/embedding-cache-ollama-2026-08-30.json)
- [只读 SQL 复验脚本](./evidence/embedding-cache-ollama-2026-08-30.sql)

## 结果边界与后续方向

- Wiki 冷、暖组是同一批文档的连续操作，不是请求体逐字节回放，属于真实线上对比而非严格 A/B。
- 百炼实验按配置的公开价格估算；账户使用免费额度时，实际账单可能为零。
- Wiki 暖调用在本轮没有降低延迟，不能把成本改善扩展解释为全指标改善。
- 同一正文更换文件名会改变文档标题及 Embedding 输入，因此被排除的控制轮次没有命中。
- 自动摘要和问题生成文本不稳定，是整轮 Embedding 命中率继续提升的主要空间。
- Wiki 实验出现两个同名 `RhinoCache` 实体页，并行页面去重仍可进一步优化。

这些限制和原始记录均被保留，没有删除不利结果。后续优化应优先建立稳定的生成阶段输入、
完善 Wiki 页面去重，并在相同负载下扩大重复实验样本，而不是放宽缓存键或调整统计口径。

## 目录说明

```text
evaluation/
├── README.md          # English documentation
├── README_CN.md       # 本文
├── baseline.json      # 绝对门槛和相对允许波动
├── evidence/          # 脱敏后的真实 Provider 实验与只读 SQL
└── fixtures/          # 确定性门禁、退化证明和缓存计算样例
```

评测 API 说明见 [`docs/api/evaluation.md`](../docs/api/evaluation.md)。
