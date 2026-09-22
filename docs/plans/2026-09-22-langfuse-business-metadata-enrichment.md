# Langfuse 业务维度上报增强设计

## 背景

WeKnora 已接入 Langfuse/LiteFuse LLM 可观测性（`internal/tracing/langfuse/`），但当前 trace 级 metadata 几乎不含业务字段：在 Langfuse 查看一条 trace，需要点进 generation 才能看到 `model_id` / `knowledge_base_id`。`agent_id / agent_name / agent_mode / channel / tenant_name / language` 等维度在业务代码里已现成可用，但从未上报。目标是让 Langfuse 端能按业务维度过滤、归因成本与诊断质量。

## 现状盘点（已上报，不再重复做）

| 维度 | 上报位置 | 状态 |
| --- | --- | --- |
| `model_id` | 7 个 generation wrapper（chat/embedding/rerank/vlm/asr），metadata `model_id` | ✅ 已做 |
| `knowledge_base_id` | `agent.execute` span `knowledge_base_ids` 数组；`retrieve` span `primary_kb_id`；检索命中详情 per-hit | ✅ 已做 |
| `call_purpose` | generation metadata（`query_rewrite`/`agent_round`/`document_summary` 等） | ✅ 已做 |
| `tenant_id` | `user.id` 兜底 `tenant:<id>`；skill/memory 相关 span metadata | ⚠️ 部分（trace 顶层缺失） |
| `session_id` / `user.id` / `http.*` / `request_id` | trace 内置 | ✅ 已做 |

## 可提取但未上报的业务维度

| 维度 | 可提取位置 | 优先级 |
| --- | --- | --- |
| `channel`（UI/API/embed/IM） | `internal/handler/session/qa.go:433` `reqCtx.channel`；IM 路径 `im/service.go` 置 `"im"` | 高 |
| `agent_mode`（quick-answer/smart-reasoning） | `types/custom_agent.go` `customAgent.Config.AgentMode` | 高 |
| `agent_id` | `qa.go:383` `buildMessageExecutionContext` 返回值 | 高 |
| `agent_name` | `customAgent.Name` | 中 |
| `tenant_name` | `types.TenantInfoFromContext`（`*Tenant.Name`） | 中 |
| `language` | `types.LanguageFromContextOrDefault` | 低 |

## 方案

### 方案 A（推荐，改动小）：handler 解析后回填 trace metadata

GinMiddleware 打开 trace 时业务信息尚未解析，因此补一条回填路径：在 `qa.go` 解析完 `reqCtx`（agentID / modelID / tenantID / channel / language 齐备）后，通过 `langfuse.TraceFromContext(ctx)` 取得 `*Trace` 句柄，把 `channel` / `agent_mode` / `agent_id` / `agent_name` / `language` 写入 trace metadata。

需要新增一个 langfuse 包内导出函数（如 `TraceFromContext(ctx).SetMetadata(...)` 或在中间件补一次 Finish 前更新），并遵循现有合并语义：内置关联字段优先，新增业务字段与现有 `mergeMetadata` 兼容。

覆盖范围：chat / agent 主线（`knowledge-chat`、`agent-chat`）。

### 方案 B（更彻底，本次不做）：扩展 trace 覆盖通道

- 扩展 `shouldTrace`（`middleware.go:117-171`）匹配 `/api/v1/embed/` 路径；
- IM 进程内调用 HTTP 化。
- 工作量较大，作为后续迭代。

### 补充（独立可落地）：`LANGFUSE_METADATA` 静态全局字段

新增环境变量 `LANGFUSE_METADATA`，值为 JSON 字典，全局附加到每条 trace 的 metadata，用于部署维度的统一标记：

```json
LANGFUSE_METADATA='{"deployment":"docker-compose","region":"cn-guangzhou","biz_line":"customer-support"}'
```

- `config.go`：`Config` 增加 `StaticMetadata map[string]interface{}`；`LoadConfigFromEnv` 读取并 `json.Unmarshal`，非法 JSON 静默忽略不阻塞启动。
- `middleware.go` GinMiddleware：`opts.Metadata` 构造后 `mergeMetadataHeader(opts.Metadata, mgr.cfg.StaticMetadata)`，复用现有合并函数（内置字段 > 请求 `X-Langfuse-Metadata` header > env 静态兜底）。
- `asynq.go`：独立 trace（无上游 traceparent）同样合并静态字段。
- 优先级：内置字段 > 请求 header > env 静态字段。动态维度（agent_id/channel 等）env 给不了，走方案 A。

## 改动清单

1. `internal/tracing/langfuse/config.go` — 新增 `StaticMetadata` 字段 + `LANGFUSE_METADATA` 解析。
2. `internal/tracing/langfuse/middleware.go` — 合并静态字段；新增 trace metadata 回填导出函数（方案 A）。
3. `internal/tracing/langfuse/asynq.go` — 独立 trace 合并静态字段。
4. `internal/handler/session/qa.go` — 解析后回填业务维度到 trace metadata。
5. 测试：`config_test.go`（env 解析合法/非法/未设置）、`middleware_test.go`（静态字段合并、内置字段不被覆盖、业务回填）。
6. 部署文件：`docker-compose.yml` 追加 `LANGFUSE_METADATA=${LANGFUSE_METADATA:-}`；`.env.example` 追加注释示例。
7. 文档：`docs/Langfuse集成.md` 环境变量表 + 第 2.4 节补充说明。

## 测试

- 补充 `LANGFUSE_METADATA` 解析测试（合法 JSON、非法 JSON 忽略、未设置为空）。
- 补充静态字段合并进 trace、内置字段不被覆盖的中间件测试。
- 补充业务字段回填 trace metadata 的测试。
- 执行相关 Go 测试、前端类型检查及差异检查。
