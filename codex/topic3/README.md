# 课题三：可复现评测、成本观测与缓存

基线固定为 WeKnora `v0.7.2`，起始 Commit：`3d5d8bfcdfeeea266b292b71cea616847af28d0f`。

## 当前实现

- 评测任务、配置、汇总指标和逐题证据持久化到数据库；服务重启后可查询，启动时把遗留的 running 任务标为失败。
- `GET /api/v1/evaluation/history?limit=20` 返回当前租户历史，原有 POST/GET 接口保持兼容。
- 聊天模型真实 usage、厂商提示词缓存字段和可选价格估算写入数据库。
- ReRank 的成功/失败、输入文档数和耗时写入同一用量表；可通过 `TOPIC3_MODEL_USAGE_ENABLED` 独立关闭统计。
- `GET /api/v1/evaluation/model-usage` 支持 `model`、RFC3339 `start`、`end` 筛选。
- 模型管理页展示真实调用数、Token、两类缓存命中率、字段覆盖率和费用覆盖。
- embedding 使用租户、模型完整配置身份、维度与原始文本 SHA-256 组成缓存键；批量部分命中只发送未命中项并恢复原顺序。
- 本地回归检查按 Recall、MRR 的绝对下降 `> 0.03` 失败；包含可验证的正常样例和人为退化负例。

真实检索退化负例仅在测试环境明确设置 `TOPIC3_EVAL_DROP_RELEVANT=true` 时启用。它会从实际检索与重排结果中删除和标准 passage 匹配的内容，再交给原指标代码计算；默认关闭，不修改指标数字，也不影响普通评测。

## 快速开始（Windows）

### 一次输入 Key，跑完整套本地正式验收

在WeKnora仓库根目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\codex\run-topic3-all.ps1
```

脚本只会隐藏询问一次本地 WeKnora API Key。Key 只存在于当前进程的请求头中，退出时清除；不会写入结果、日志或 Git。程序依次完成免费预检、Embedding 缓存 9 轮、Wiki 真实对照 8 次、真实检索退化负例和最终核验。任何付费阶段失败都会立即停止，不自动重试。

成功时终端最后显示 `ALL_COMPLETE`。完整证据位于 `codex/topic3/results/batch-<时间>/`，包括检查点、两份实验报告、负例报告、JSON/Markdown 验收报告和日志。此命令不会重复生成或覆盖正式基线，也不处理 GitHub 推送和空数据库复现。

当前修订版启动行应显示 `runner=2026-09-08.3`。程序会在每次切换缓存或退化模式后，读取容器内对应的非敏感配置并核对；配置没有真正生效时会在模型调用前停止。若最新失败批次已经完整通过9轮缓存实验、仅因 Wiki 前缀不足1024 Token停止，本版本会严格校验并复用9轮结果，只继续Wiki和后续阶段。

这是正式付费批次：共包含 9 次 10 题评测、8 次 Wiki 调用和 1 次 10 题退化评测。只有准备正式运行时才执行。

```powershell
Copy-Item .env.example .env
# 检查 .env；密钥不要提交到 Git
powershell -ExecutionPolicy Bypass -File codex/topic3/topic3.ps1 up
```

本机已经固定以下资源：

- 知识库：`22459db3-db22-4682-9e9b-0b06a382f533`
- 对话模型：`8b9e4baa-ed26-4bd2-a940-fd51d7390a69`
- ReRank 模型：`63a4ddf0-f689-42a1-8208-9ca57a234234`
- Embedding：`text-embedding-v4`，1024维

最低成本1题评测只需运行下列命令。脚本会隐藏输入 API Key，并自动使用以上固定 ID：

```powershell
powershell -ExecutionPolicy Bypass -File codex/topic3/topic3.ps1 eval
```

不要把 API Key 写入命令、聊天或仓库。非本机环境仍可通过 `TOPIC3_*` 环境变量覆盖所有 ID。

评测结果写入 `codex/topic3/results/`。2026-09-07 已由用户确认最新的真实 10 题结果，并冻结为 `codex/topic3/baseline.json`；文件保留源结果路径、SHA-256 和评测快照，候选代码无权自动覆盖基线。

正式证据冻结后可完全离线核验哈希、轮数、指标、负例和密钥扫描：

```powershell
python codex/topic3/verify_evidence.py
```

成功结果必须为 `EVIDENCE_OK`。

```powershell
powershell -ExecutionPolicy Bypass -File codex/topic3/topic3.ps1 check -Current codex/topic3/results/<运行ID>.json
powershell -ExecutionPolicy Bypass -File codex/topic3/topic3.ps1 cache-bench
```

正式缓存实验会真实运行9次10题评测，必须同时设置：

```powershell
$env:TOPIC3_DATASET_ID = 'topic3-10'
$env:TOPIC3_CACHE_BENCH_CONFIRM = 'YES'
powershell -ExecutionPolicy Bypass -File codex/topic3/topic3.ps1 cache-bench
```

实验顺序固定为缓存关闭3次、冷缓存3次、暖缓存3次。冷缓存只删除当前知识库租户下、`source.json` 中问题和语料精确哈希对应的缓存行，不会清空整张表。命令结束后强制恢复为缓存开启。

`make topic3-up/eval/check/cache-bench` 是跨平台等价入口。

## 固定评测数据

- `default` 是仓库自带的 1 题数据集，仅用于最低成本冒烟测试。
- `topic3-10` 是课题三正式固定数据集，包含 10 道题及稳定的题目、语料和答案 ID。
- 可审阅源数据位于 `codex/topic3/datasets/topic3-10/source.json`；运行 `python codex/topic3/generate_dataset.py` 可重新生成五份 Parquet。
- 运行正式 10 题前设置 `$env:TOPIC3_DATASET_ID = 'topic3-10'`。首次真实结果必须人工核对后才能成为基线。
- Windows 可直接运行 `powershell -ExecutionPolicy Bypass -File codex/topic3/topic3.ps1 eval -Dataset topic3-10`，无需单独设置数据集环境变量。

## 费用配置

在 `.env` 中按实验当天官方价格填写：

```dotenv
TOPIC3_CHAT_INPUT_PRICE_PER_MILLION_CNY=
TOPIC3_CHAT_CACHED_INPUT_PRICE_PER_MILLION_CNY=
TOPIC3_CHAT_OUTPUT_PRICE_PER_MILLION_CNY=
```

价格缺失时 `cost_known_calls` 不增加；页面不会把未知费用冒充成零覆盖。累计预算须另按百炼账单复核，达到 80 元停止追加自动实验，保留 20 元结算余量。

## CI 与合并门禁

`.github/workflows/topic3-regression.yml` 支持 PR 自动检查和手动运行，不再定时运行。正常的退化fixture会被检查器拒绝、但确定性任务保持绿色。手动勾选 `run_negative_demo` 可产生专门的预期红灯证据。

真实评测不会由PR或定时任务触发。只有手动运行时同时勾选 `run_real_evaluation`，并配置仓库变量 `TOPIC3_REAL_EVAL_ENABLED=true`、模型 ID 及 Secret `TOPIC3_TOKEN`，才会产生真实调用和费用。

Fork 的 `topic3-v0.7.2-base` 已启用分支保护，`topic3-regression / deterministic-checks` 已设为 required status check。负例演示提交 `c159b29c` 使 Required 检查和专用负例任务同时失败，页面禁止合并；恢复提交 `e18741f2` 后检查重新变绿并允许合并。远程门禁证据见 `codex/topic3/CI_ACCEPTANCE_20260908.md`。

## 状态边界

| 项目 | 状态 |
|---|---|
| 数据库迁移与后端实现 | 已实现，迁移版本 83，专项单元测试通过 |
| 回归检查正常/退化负例 | 已真实验收；正式负例 Recall/MRR 从 1.0 降至 0，并被门禁拒绝 |
| 前端统计区 | 已实现，类型检查与生产构建通过；真实调用与数据库汇总已核对 |
| Docker 从零启动 | 已完成首次构建；五个容器健康运行，迁移版本 83；最终空数据库复现仍待完成 |
| 百炼真实问答、usage 与费用 | 对话、embedding、ReRank 均已真实调用并入库；对话已知费用约 0.0902 元，Embedding/ReRank 精确费用待账单复核 |
| 固定 10 题数据集与正式基线 | 两次真实 10/10 评测均成功；最新结果 Recall/MRR/NDCG 均为 1.0，已经人工确认并冻结为正式基线 |
| embedding 冷/暖缓存三组重复实验 | 已完成9轮真实验收；真实调用减少约91.7%，检索质量不变 |
| Wiki 厂商缓存前后实验 | 已完成8次真实调用；输出质量不变，但本次隐式缓存均未命中 |
| GitHub required check | 已配置并真实验证；退化提交被阻断，恢复后检查通过 |

任何尚未完成的真实验收不得以 fixture、mock 或本地逻辑测试替代。
