# WeKnora Wiki 评测系统迁移指南

本文用于把 `wiki_eval` 迁移到另一套 WeKnora 部署、另一台开发机，或另一条 CI 回归流水线。目标是先确认评测系统和目标服务的契约一致，再让结果具备可比性。

这是一套确定性端到端 Wiki 评测，不是通用 LLM-as-a-judge。它主要验证页面、实体、文本事实、链接图、搜索、增量更新与删除回收行为。

## 1. 迁移边界

必须一起迁移的内容：

```text
wiki_eval/
  eval_weknora.py                 # 主评测入口
  requirements.txt                # Python 依赖
  config.example.yaml             # 配置模板
  datasets/                       # 文档与 gold 标注
  tools/validate_stardust_corpus.py
  EVAL_SPEC.md                    # 指标口径
  RUN_MANUAL.md                   # 本地运行参考
```

建议保留（但不应把它们当作新的基准结果）：

```text
wiki_eval/reports/                # 历史报告和 raw API 证据
wiki_eval/bugfix_log_2026-07-28_CN.md
```

不要迁移测试运行产生的测试 KB。每个新环境应创建自己的测试 KB，避免环境数据、模型版本或旧任务残留污染结果。

## 2. 目标环境前置条件

### 2.1 Python 与依赖

推荐 Python 3.10 或更高版本。Windows PowerShell 示例：

```powershell
cd D:\path\to\repo\wiki_eval
python -m venv .venv
.\.venv\Scripts\Activate.ps1
python -m pip install -r requirements.txt
```

WSL/Linux 示例：

```bash
cd /path/to/repo/wiki_eval
python3 -m venv .venv
source .venv/bin/activate
python3 -m pip install -r requirements.txt
```

当前依赖只有 `requests` 与 `PyYAML`。评测脚本本身不要求 DeepEval、Ragas 或外部 judge。

### 2.2 服务与认证

运行机必须能访问目标 WeKnora 服务。准备下列环境变量：

```bash
WEKNORA_BASE_URL=http://localhost:8080
WEKNORA_API_PREFIX=/api/v1
WEKNORA_TOKEN=<Bearer token>                 # Token 认证时使用
# 或
WEKNORA_API_KEY=<API key>                     # API Key 认证时使用
WEKNORA_API_KEY_HEADER=X-API-Key              # 可按服务实际 header 调整
```

创建新知识库并运行导入时，还需要目标环境中的模型 ID：

```bash
WEKNORA_EMBEDDING_MODEL_ID=<uuid>
WEKNORA_SUMMARY_MODEL_ID=<uuid>
WEKNORA_WIKI_SYNTHESIS_MODEL_ID=<uuid>        # 可选；服务要求时提供
```

Token、API key 与模型 ID 不要提交到仓库。用 CI Secret、部署环境变量或本机私有 `.env` 注入。

### 2.3 Windows、WSL 与容器网络

- Windows 本机访问服务时，通常使用 `http://localhost:8080`。
- WSL 内访问 Windows 服务时，先确认 `localhost` 是否转发；不通时用宿主机可达地址。
- Docker/CI 中的 `localhost` 指向当前容器或 runner，不是宿主机；应使用服务名、同一网络的地址或明确的端口映射。

迁移完成前先用目标运行环境实际执行一次 API 连通性检查，不要仅以浏览器能打开服务作为依据。

## 3. API 兼容性检查

主脚本依赖的具体路由以 `eval_weknora.py` 为准。迁移到不同分支或不同产品版本前，至少核对以下能力。

| 能力 | 用途 | 兼容要求 |
|---|---|---|
| 创建知识库 | 建立隔离的评测 KB | 能创建启用 Wiki/图谱的 document KB，并接受模型配置 |
| 手工导入知识 | 导入 `docs_v1`、`docs_v2`、`docs_del` | 能返回知识文档 ID，并触发 Wiki ingest |
| Wiki 页面 | 页面/实体/事实/链接评测 | 可列出页面，页面至少含 `slug`、`title`、`content`、`source_refs`、`in_links`、`out_links` |
| `/wiki/graph` | 图节点和图边趋势指标 | 返回可识别的节点和 `source`/`target` 端点边，或在脚本中适配新 schema |
| `/wiki/search` | Recall@k、MRR | 返回可识别的页面 slug/title 或等价标识 |
| `/wiki/lint`、`/wiki/issues` | 采集链接健康证据 | 可读取 lint/issue 结果；字段变化应记录并适配 |
| `/wiki/auto-fix` | 删除后的恢复性对照 | 可手动触发，且调用方有权限 |
| delete/retract | 删除回归 | 删除文档后能观察到 Wiki 的异步调和最终状态 |
| `/sessions`、`/knowledge-chat/{session_id}` | 可选 QA | 仅 `--run-qa` 需要；请求体不兼容时用配置模板覆盖 |

页面字段兼容尤其重要：`source_refs`、`in_links`、`out_links` 是删除与链接评测的证据来源。若目标服务只返回一部分字段，不能把缺字段默认为“清理成功”；应先修改适配器或将该项标记为不可评。

## 4. 配置目标环境

从样例创建本地配置：

```powershell
Copy-Item config.example.yaml config.yaml
```

最小配置例子：

```yaml
base_url: http://localhost:8080
api_prefix: /api/v1
http_timeout: 60

knowledge_base:
  name: wiki-eval-stardust
  type: document
  indexing_strategy:
    vector_enabled: true
    keyword_enabled: true
    wiki_enabled: true
    graph_enabled: true
```

通过命令行覆盖地址，便于同一份配置在多环境复用：

```bash
python eval_weknora.py --config config.yaml --base-url http://target-host:8080 --api-prefix /api/v1
```

不要把真实密钥写进 `config.yaml`。配置文件适合放非敏感地址、超时、KB 名称和 QA payload 模板。

## 5. 数据集迁移与适配

### 5.1 保持目录结构

每个数据集应具有如下结构：

```text
datasets/<dataset-name>/
  docs_v1/              # 基线文档
  docs_v2/              # 增量文档
  docs_del/             # 删除场景中的目标文档
  gold/
    entities.json
    facts.json
    relations.json
    search_cases.json
    qa.json
    update_events.json
    delete_events.json
```

不同数据集可以只启用其需要的可选场景，但基线评测依赖实体、事实和关系 gold。新增或迁移 Stardust 语料后，先严格校验：

```bash
python wiki_eval/tools/validate_stardust_corpus.py --dataset wiki_eval/datasets/stardust --strict
```

预期为 `errors: 0`、`warnings: 0`。在 CI 中也应以该命令为前置检查。

### 5.2 重新对齐 slug 与别名

Gold 的 `expected_slug`、实体别名和页面归属隐含了 Wiki 的命名策略。迁移到另一种生成器或另一版本的 slug 规则时：

1. 先导入一份小语料，保存 `raw/wiki_pages.json`。
2. 对照实际 `slug`、`title` 与 gold 实体名。
3. 仅在“同一实体、不同规范命名”的情况下更新别名或预期 slug。
4. 不要为掩盖漏抽取、错误合并或页面错分而随意扩大别名集合。

Gold 必须描述语义真值，而非为了让当前实现得分更高而反向改写。

### 5.3 删除场景要同步维护

新增删除 case 时，至少同步更新：

- `docs_del/` 中的文档；
- `gold/delete_events.json`；
- 若该数据集使用 seed graph，则同步其 JSON 和 Markdown 说明。

Delete 断言应区分“必须移除”“必须保留”“允许改写”。这能避免把合理的页面重写误判成失败。

## 6. 推荐迁移验收流程

### 6.1 第一轮：基础可用性

```bash
python wiki_eval/tools/validate_stardust_corpus.py --dataset wiki_eval/datasets/stardust --strict
python wiki_eval/eval_weknora.py --config wiki_eval/config.yaml
```

检查最新 `wiki_eval/reports/run_<timestamp>/report.md` 与 `metrics.json`：

- 任务是否在超时前稳定完成；
- 是否真正创建了测试 KB 并导入了所有基线文档；
- raw 响应中页面字段是否满足评测契约；
- 失败是否来自产品行为，而不是 401、404、payload schema 不匹配或异步任务尚未完成。

### 6.2 第二轮：更新与删除

```bash
python wiki_eval/eval_weknora.py --config wiki_eval/config.yaml --run-update
python wiki_eval/eval_weknora.py --config wiki_eval/config.yaml --run-delete
```

删除评测先采集原生 delete/retract 完成后的状态，随后调用 `POST /knowledgebase/{kb_id}/wiki/auto-fix`，并单独采集 AutoFix 后的状态。报告中的两组指标含义不同：

| 指标前缀 | 含义 |
|---|---|
| `delete_*` | 删除/retract 路径自身的正确性，是主判据 |
| `delete_autofix_*` | 手工 AutoFix 后的恢复性对照，不替代原生删除正确性 |

例如，`delete_must_remove_in_links_rate` 低而 `delete_autofix_must_remove_in_links_rate` 高，说明删除路径留下了可被手动修复的悬链。它仍然是删除链路的问题，只是 AutoFix 能作为人工安全网恢复。

### 6.3 第三轮：可选 QA

```bash
python wiki_eval/eval_weknora.py --config wiki_eval/config.yaml --run-qa
```

只有目标部署兼容会话和 chat payload 时才启用。若不兼容，在 `config.yaml` 中提供 `session_create_payloads` 与 `knowledge_chat_payload_template`；不能通过臆测字段名让 QA 静默失败。

## 7. 复用既有知识库

已有稳定、隔离的测试 KB 时，可跳过导入：

```bash
export WEKNORA_KB_ID=<existing-kb-id>
python wiki_eval/eval_weknora.py --skip-ingest --existing-kb-id "$WEKNORA_KB_ID"
```

此模式适合调试读取类指标或缩短 CI 时间，但不适合验证 ingest、update、delete 的端到端行为。用于基线对比时，必须记录 KB 的构建时间、数据集版本、WeKnora commit/镜像版本和模型版本。

## 8. CI 建议

1. 先运行语料严格校验，再运行评测。
2. 使用专用测试租户或每次创建有唯一前缀的 KB，禁止连到生产 KB。
3. 收集并保留 `report.md`、`metrics.json` 与 `raw/`，它们是定位回归的证据。
4. 为异步 Wiki 任务预留足够的 `WEKNORA_EVAL_TIMEOUT_SEC`、`WEKNORA_EVAL_INTERVAL_SEC`；慢环境不要用“提前结束”换取假阴性。
5. 将服务版本、模型 ID（可脱敏）、数据集 revision 与配置快照写入 CI artifact 或报告元数据。
6. 接口 schema 或指标定义发生变化时，标记为新的基线版本；不要直接与旧指标数值做机械比较。
7. 把 `graph_edge_recall_heuristic` 当趋势信号，不当作带 predicate 语义的知识图谱关系召回。当前图接口只有链接端点时，它衡量的是实体对的连接覆盖，而不是 `uses`、`operated_by` 等关系类型是否正确。

## 9. 常见故障与处理

| 现象 | 优先检查 |
|---|---|
| 连接失败或 404 | `base_url`、`api_prefix`、容器/WSL 网络与目标路由版本 |
| 401/403 | Token/API key、header 名称、AutoFix 权限 |
| 创建 KB 失败 | 三个模型 ID、模型是否在该租户可见、Wiki/graph 开关 |
| 一直等待 Wiki 完成 | 后端队列、worker、模型服务、超时与轮询配置 |
| 页面数为零或字段缺失 | manual ingest 路由、Wiki 开关、页面 API schema |
| delete 结果不稳定 | 异步 retract 尚未收敛；检查 raw/delete 中各轮请求与最终页面状态 |
| AutoFix 调用失败 | `/wiki/auto-fix` 路由、调用权限、服务版本；保留原生 delete 结果并明确记录 AutoFix 不可用 |
| graph 指标异常高或低 | 检查 graph 原始 payload 和实体映射；不要把无谓词链接边解释为关系正确率 |

## 10. 交付检查表

- [ ] `validate_stardust_corpus.py --strict` 通过。
- [ ] 目标环境认证、模型 ID 与 Wiki worker 可用。
- [ ] 基线报告中页面 API 字段完整，raw 产物已保存。
- [ ] update 与 delete 均已单独复跑。
- [ ] 原生 delete 指标和 AutoFix 对照指标已分别查看。
- [ ] 使用的服务版本、模型版本、数据集 revision 和配置已记录。
- [ ] 若接口或指标口径不同，已先完成脚本适配并建立新基线。

## 11. 相关文档

- [运行手册](./RUN_MANUAL.md)：本仓库/WSL 中的常规复跑命令。
- [评测规格](./EVAL_SPEC.md)：指标和数据集的详细口径。
- [配置样例](./config.example.yaml)：非敏感配置模板。