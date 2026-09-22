# WeKnora Onboarding 指南

> 本文档由 `/understand` + `/understand-onboard` 基于知识图谱（`.ua/knowledge-graph.json`）自动生成。
> 图谱基于 commit `0dd50a7`；若当前 HEAD 已前进，部分内容可能未覆盖最新改动，运行 `/understand` 可刷新。

## 1. Project Overview

| 项 | 值 |
|---|---|
| **名称** | WeKnora（`github.com/Tencent/WeKnora`） |
| **描述** | 开源的 LLM 驱动知识框架，面向企业级文档理解、语义检索与自主推理：RAG 快速问答、ReAct Agent（编排检索 + MCP 工具 + 租户技能目录 + Docker/E2B/Cube 沙箱）、Auto-Wiki |
| **语言** | Go（主体）、TypeScript、Vue、Python、SQL、Shell、Protobuf、Rust（AnyDoc）等 23 种 |
| **框架** | Gin · GORM 风格仓储 · Vue 3 + Vite · Starlette/pydantic（docreader）· Docker/Compose · GitHub Actions |
| **规模** | 2539 文件（分析范围）· 13492 节点 · 114614 边 |

## 2. Architecture Layers（10 层）

| 层 | 节点 | 职责 |
|---|---|---|
| **API 与渠道接入层** | 189 | Gin 路由与 handler、认证/RBAC 中间件、微信/钉钉/飞书 IM 渠道适配 |
| **Agent 引擎与沙箱层** | 200 | ReAct 推理循环、工具调用、上下文压缩、意图网关、MCP 客户端、Docker/E2B 沙箱 |
| **业务服务层** | 484 | 知识库/会话/数据源/记忆/任务编排，container 组合根，chat_pipeline 管线 |
| **数据与检索层** | 494 | 336 张表 + 397 个迁移文件；Milvus/ES/Qdrant/Postgres/Neo4j 等 11 种检索后端仓储 |
| **共享类型与契约层** | 147 | `internal/types` 领域模型、接口契约、错误码 —— 全项目最高扇入 |
| **前端界面层** | 710 | Vue 3 控制台（views/stores/composables/api）+ 微信小程序 |
| **文档解析与 MCP 微服务层** | 116 | Python gRPC docreader、独立 MCP Server、AnyDoc (Rust) |
| **文档与示例层** | 169 | README/架构文档、VitePress 站点（website-docs）、示例工程 |
| **基础设施与 CI/CD 层** | 135 | Docker/Helm、GitHub Actions、构建脚本 |
| **配置层** | 24 | `config/`（builtin_agents、builtin_models、prompt_templates）、go.mod、.env |

## 3. Key Concepts

- **组合根手工依赖注入**：`internal/container/container.go` 以手工 DI 初始化 DB/Redis/检索引擎/全部服务 —— 全图第一枢纽（868 连接）。理解它就理解了对象装配。
- **ReAct Agent 循环**：`think.go → act.go → observe.go` 三段式驱动，`engine.go` 负责系统提示词、工具与技能装配、迭代预算。
- **多租户贯穿**：`tenant` 是高频标签（256）—— 租户上下文经 `context_helpers.go` 的 With/From 对在全链路传递。
- **检索抽象**：`internal/types/interfaces` 定义契约，`repository/retriever/` 下 11 种向量库各一个仓储实现，可插拔替换。
- **chat_pipeline 插件化**：检索/去重/重排/实体搜索拆成管线插件（search.go、rerank.go、merge.go…）。
- **docreader 微服务**：Python gRPC 独立进程做文档解析，主服务经 proto 契约调用 —— 跨语言边界。
- **安全标签密集**：`security` 544 次、`policy` 69 次 —— 认证分流（JWT/API Key/外部用户）与 RBAC 是一等公民。

## 4. Guided Tour（12 步）

1. **项目概览** — `README.md`
2. **服务端入口与装配** — `cmd/server/main.go` → `bootstrap.go` → `internal/container/container.go`
3. **路由与鉴权中间件** — `router/router.go` · `routes_chat.go` · `middleware/auth.go` · `rbac.go`
4. **共享类型与契约层** — `internal/types/`（knowledgebase、agent、interfaces）
5. **业务服务与 RAG 管线** — `agent_service.go` · `chat_pipeline/search.go` · `knowledge_process.go`
6. **Agent 引擎与 ReAct 循环** — `engine.go` · `think.go` · `act.go` · `observe.go`
7. **数据库 Schema 与迁移** — `migrations/sqlite/000000_init.up.sql`（knowledge_bases、chunks 表）
8. **向量检索与多后端仓储** — `retriever/milvus/repository.go` · `elasticsearch/v8/repository.go`
9. **文档解析微服务与 gRPC 契约** — `docreader/main.py` · `docreader/proto/docreader.proto`
10. **前端界面与路由守卫** — `frontend/src/main.ts` · `router/index.ts` · `App.vue`
11. **运行配置与提示词模板** — `config/config.yaml` · `prompt_templates/` · `builtin_agents.yaml` · `go.mod`
12. **容器化、CI/CD 与协作规范** — `docker-compose.yml` · `Dockerfile.app` · `app.yml` · `AGENTS.md` · `SECURITY.md`

## 5. File Map（按层）

**API 层**：`internal/router/router.go`（总装配，API Key 策略自检）· `internal/middleware/auth.go`（五路认证分流）· `internal/handler/*`（按域拆分 handler）

**Agent 层**：`internal/agent/engine.go`（引擎/ReAct 主循环）· `internal/agent/tools/*`（agent-tool 标签 132 个）· 沙箱相关（sandbox 标签 776）

**服务层**：`internal/container/container.go`（组合根）· `internal/application/service/*`（chat_pipeline、knowledge_process、agent_service）· `cmd/*/main.go` 三个入口

**数据层**：`migrations/*`（336 表）· `internal/application/repository/*`（仓储 + 11 个 retriever）

**类型层**：`internal/types/*.go`（tenant、faq、agent、memory、vectorstore… 领域模型全在这）

**前端**：`frontend/src/views/*`（页面）· `stores/`（状态）· `api/`（请求封装）· `router/index.ts`（守卫）

**微服务**：`docreader/main.py`（gRPC Servicer）· `docreader/proto/docreader.proto` · `mcp-server/`

**配置**：`config/config.yaml`（运行时）· `config/prompt_templates/`（提示词）· `config/builtin_agents.yaml`

## 6. Complexity Hotspots（谨慎进入）

复杂度分布：**complex 795 · moderate 951 · simple 922**。最需要小心的：

| 文件 | 为什么危险 |
|---|---|
| `internal/container/container.go` | 868 连接的全图第一枢纽，手工 DI，改任何服务构造都要动它 |
| `internal/types/tenant.go` 等 `internal/types/*` | 六个 650+ 连接的类型文件（tenant/faq/datasource/agent/memory/context_helpers）—— 高扇入，改字段波及全仓 |
| `internal/types/context_helpers.go` | 全项目跨层传元数据的基础设施，With/From 对不能错 |
| `retriever/{milvus,elasticsearch,neo4j,…}/repository.go` | 11 个 complex 级向量库仓储，各自协议细节不同 |
| `chat_pipeline/{search,rerank,merge,chat_completion_stream}.go` | 流式管线，时序与去重逻辑微妙 |
| `cmd/desktop/main.go` / `update.go` | 桌面端入口，与主服务代码路径独立 |
| `internal/agent/tools/data_analysis.go` | complex 级 agent 工具 |
