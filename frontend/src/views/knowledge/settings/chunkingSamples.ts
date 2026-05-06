// Curated sample texts for the chunking debug drawer. Each preset is sized
// to ≈2000–4000 characters and shaped to exercise a distinct tier of the
// chunker, so users can quickly see how their config behaves on realistic
// content without preparing their own sample.

export interface ChunkingSample {
  id: string
  // i18n key under knowledgeEditor.chunking.debug.samples.<id>
  labelKey: string
  text: string
}

const MARKDOWN_SAMPLE = `# WeKnora 知识框架

WeKnora 是一个基于 LLM 的开源企业知识框架，集 RAG 问答、ReAct 智能体、Wiki 知识图谱于一体。本文介绍其设计动机、架构与典型用法。

## 设计动机

企业内部知识散落在 Confluence、飞书、Notion、Git 仓库等多种系统，传统全文检索难以理解语义，单一 LLM 又缺少可信赖的上下文来源。WeKnora 的目标是：

- **多源接入**：把分散内容统一抽取、清洗、向量化
- **多策略检索**：稠密、稀疏、知识图谱多路召回 + RRF 融合
- **可解释推理**：ReAct Agent 让推理链条可追踪、可干预
- **可观测**：原生集成 Langfuse，每一步推理、每一次工具调用都有 trace

## 核心特性

### 多模型支持

兼容 20+ 主流 LLM：OpenAI、Anthropic、DeepSeek、Qwen、智谱、混元、Gemini，以及本地 Ollama。模型层抽象 \`Provider\` 接口，新增厂商只需实现 \`Chat()\` 方法并注册即可。

### 多向量库支持

通过 \`RETRIEVE_DRIVER\` 环境变量切换：

| 驱动 | 适合场景 |
|------|---------|
| pgvector | 已有 PostgreSQL，规模 < 1000 万 chunk |
| Elasticsearch | 需要混合检索 + 关键词高亮 |
| Milvus | 大规模、低延迟 |
| Weaviate | 需要混合 BM25 + GraphQL 查询 |
| Qdrant | 默认推荐，部署简单、性能均衡 |

### 数据源连接器

- **飞书**：自动同步知识库与文档树，支持增量更新
- **Notion**：基于 API 抽取 Page / Database
- **语雀**：按 Group 拉取，保留多级目录

每个连接器实现统一的 \`DataSource\` 接口，新增数据源约 200 行代码。

## 快速开始

### 环境要求

- Docker 20.10+ 与 Docker Compose v2
- 至少 8GB 内存、20GB 磁盘空间
- 如需本地运行 LLM，建议 NVIDIA GPU 24GB 以上显存

### 启动命令

\`\`\`bash
git clone https://github.com/Tencent/WeKnora && cd WeKnora
cp .env.example .env       # 修改你的模型与数据库配置
make dev-start             # 启动 postgres / redis / qdrant
make dev-app               # 启动后端，热重载
make dev-frontend          # 启动前端，自动刷新
\`\`\`

打开浏览器访问 http://localhost:5173 即可。

## 架构概览

WeKnora 后端采用清晰的分层架构：

\`\`\`
┌────────────────────────┐
│   HTTP 层 (Gin)        │  请求路由 / 鉴权 / 限流
├────────────────────────┤
│   业务层 (service)     │  核心算法 / 编排
├────────────────────────┤
│   数据层 (repository)  │  数据库访问 / 缓存
├────────────────────────┤
│   领域模型 (types)     │  KnowledgeBase / Chunk / Agent ...
└────────────────────────┘
\`\`\`

依赖注入容器（go/dig）在 \`cmd/server/main.go\` 装配所有依赖，handler 通过接口拿到 service，service 通过接口拿到 repository，所有可替换实现都在启动时绑定。

### 检索流水线

文档上传 → docreader 服务解析 → 分块（含父子分块） → 嵌入 → 写入向量库与图数据库 → 用户查询时多路召回 → RRF 融合 → 重排 → 返回。

### Agent 编排

Agent 引擎执行经典 ReAct 循环：

1. **Reason**：LLM 根据系统提示和当前观察输出推理
2. **Act**：解析工具调用 JSON，分发给内置工具或 MCP 工具
3. **Observe**：把工具返回值塞回上下文
4. 重复 1-3 直到产出最终答案或达到最大迭代

每一步都通过 SSE 流式返回前端，并写入 Langfuse trace 便于排查。

## 进一步阅读

- API 文档：\`docs/api/README.md\`
- 配置项清单：\`config/config.yaml\` 与 \`.env.example\`
- 故障排查：\`docs/QA.md\`
- 路线图：\`docs/ROADMAP.md\``

const FAQ_SAMPLE = `# WeKnora 部署与使用 FAQ

本文档汇总社区与内部用户最常问到的问题，按"安装 / 配置 / 检索 / 模型 / 性能"分类。

## 安装与启动

### Q1: Docker 镜像在哪里下载？
官方镜像通过 daocloud 加速分发：

\`\`\`
docker pull docker.m.daocloud.io/wechatopenai/weknora-app:v0.5.0
docker pull docker.m.daocloud.io/wechatopenai/weknora-docreader:v0.5.0
docker pull docker.m.daocloud.io/wechatopenai/weknora-ui:v0.5.0
\`\`\`

### Q2: 启动后访问 5173 显示旧界面？
浏览器缓存了旧版前端资源。Ctrl+Shift+R 强制刷新即可，必要时清空站点存储。

### Q3: 注册新用户报 500 Internal Server Error
通常是后端容器未正常启动。先检查 \`make dev-logs | grep app\`，常见为数据库连接失败或迁移未执行。

### Q4: 上传文档报 column "xxx" does not exist
数据库迁移未完成。运行 \`make migrate-up\`，确认所有迁移成功后重启 app 容器。

### Q5: 文档上传后状态一直是 processing
检查 docreader 容器是否运行（\`docker compose ps\`），并查看其日志中是否有解析报错。常见原因：依赖的 Python 包缺失、PDF 加密、扫描件 OCR 超时。

## 配置

### Q6: 如何切换向量库？
修改 \`.env\` 中的 \`RETRIEVE_DRIVER\`，可选值：\`qdrant\` (默认) / \`pgvector\` / \`elasticsearch\` / \`milvus\` / \`weaviate\`。切换后需要重新嵌入存量数据。

### Q7: 如何修改默认 chunk 大小？
全局默认在 \`config/config.yaml\` 的 \`knowledge.chunking\` 段；单个知识库可在 UI 的"分块设置"页覆盖。修改后仅对新上传文档生效。

### Q8: 如何启用多模态？
在知识库的"多模态配置"中开启，并选择一个 VLM 模型（如 GPT-4o、Qwen2.5-VL）。开启后，文档中的图片会被 VLM 描述并参与检索。

### Q9: 怎么把日志改成 JSON 格式？
设置 \`LOG_FORMAT=json LOG_LEVEL=info\`，便于接入 ELK / Loki 等日志栈。

## 检索与召回

### Q10: 检索结果为空但确认文档已索引
按这个顺序排查：

1. 确认知识库状态为 \`completed\`，而不是 \`indexing\` 或 \`failed\`
2. 检查向量库连接（默认 qdrant:6334），\`docker compose ps\` 看 qdrant 是否健康
3. 在管理页"重建索引"，确认嵌入模型工作正常
4. 查看后端日志中 retriever 是否报 dimension mismatch

### Q11: 检索结果命中但答非所问
通常是 chunk 边界切割了关键句。尝试：

- 启用父子分块，让向量召回小块、上下文用大块
- 调高 chunk overlap 到 chunk size 的 15%-20%
- 切换到"按标题切分"或"结构感知"策略

### Q12: 排序看起来不合理
重排（rerank）模型可能未启用。在"模型设置"中配置 BGE-Reranker 或 Cohere Rerank，并在知识库配置中打开重排。

## 模型与 Token

### Q13: 调用 OpenAI 超时
检查代理网络与 \`OPENAI_BASE_URL\` 配置；本地无外网时切换到 Ollama 或国内大模型。

### Q14: token 用量在哪里看？
开启 Langfuse 后，每个会话、每次推理、每个工具调用的 token 消耗都有完整 trace，可按时间、租户、模型聚合。

### Q15: 如何接入私有 LLM？
实现 \`internal/models/chat/provider/Provider\` 接口，注册到 provider 工厂，配置文件中加一条记录即可，约 100-200 行代码。

## 性能

### Q16: 大文档上传慢怎么办？
docreader 是 CPU 密集型，建议横向扩容：在 docker-compose 中调高 docreader 服务的 \`deploy.replicas\`，或独立部署到更强的机器。

### Q17: 嵌入吞吐瓶颈
切换到本地 Ollama + GPU 加速，或用商用 API 的批量端点（如 OpenAI 的 batch API）。设置 \`EMBED_BATCH_SIZE=64\` 显著提升吞吐。

### Q18: 数据库慢查询
启用 \`pg_stat_statements\`，定位慢 SQL；常见瓶颈是 chunks 表的元数据 JSONB 查询，可针对热点字段加索引。`

const CHAPTER_SAMPLE = `第 1 章 引言

1.1 文档目的

本文档描述某分布式知识检索平台的整体架构、组件划分与部署方案，面向运维工程师、SRE 与系统架构师。读者应熟悉 Docker、Kubernetes、PostgreSQL 等基础组件。

1.2 名词约定

- 网关层（Gateway）：负责入口流量调度与协议转换
- 业务层（Service）：承载领域逻辑与编排
- 存储层（Storage）：封装持久化与缓存细节
- 推理层（Inference）：与外部 LLM / 嵌入模型交互的隔离层

1.3 文档版本

本版本 v1.4，对应平台代码 v0.5.x 系列。文档随代码同步更新，主要变更见附录 A。

第 2 章 系统架构

2.1 总体设计

平台采用经典的微服务架构，但在边界划分上保持克制——只在确有独立伸缩需求的位置切分服务。当前共 4 个长驻服务：网关、业务后端、文档解析、向量索引。

2.2 模块划分

2.2.1 用户与权限

用户、组织、共享空间、角色与权限规则集中在 user-service 中。RBAC 模型支持继承与覆盖，外部 OIDC 接入通过适配层完成。

2.2.2 内容与索引

内容侧负责文档生命周期：上传、解析、分块、嵌入、入库、检索、引用追踪。Chunk 是最细粒度的检索单元，每个 chunk 携带源文档元数据与位置信息。

2.2.3 推理与编排

Agent 编排器负责 ReAct 循环，工具调用通过 MCP 协议或内置注册表分发。所有 LLM 调用统一经过模型代理层，方便切换、限流与计费。

2.3 数据流

文档上传后依次经过：解析 → 清洗 → 分块 → 嵌入 → 写库。查询路径：rewrite → 多路召回 → fusion → rerank → context 拼接 → LLM 生成。

第 3 章 部署指南

3.1 环境要求

物理资源：8 vCPU / 16 GB 内存 / 100 GB SSD 起步；生产环境建议 16 vCPU / 32 GB / 500 GB。GPU 仅在本地推理时必需。

3.2 容器编排

3.2.1 单机 Docker Compose

适合 PoC 与中小团队（< 100 人，< 100 万 chunk）。一条命令拉起全部服务：

docker compose up -d

3.2.2 Kubernetes Helm

适合规模化部署。Helm chart 在 helm/ 目录，包含 statefulset、配置 secret、ingress 模版。可与现有 PG / Redis 集群对接。

3.3 配置最佳实践

- 把外部依赖（数据库、对象存储、向量库）放在配置中心，避免硬编码
- 模型 API key 用 Secret 管理，按环境隔离
- 日志走 stdout/stderr 由编排平台采集，避免本地落盘

第 4 章 常见运维场景

4.1 升级流程

蓝绿或滚动均可。升级前必看 CHANGELOG 中是否有 schema 变更，迁移先于业务镜像替换。

4.2 备份与恢复

PG 全量每日备份 + WAL 增量；MinIO/对象存储依赖云端版本管理。Qdrant 用 snapshot 接口定期落盘到对象存储。

4.3 故障排查

按"网关 → 业务 → 存储 → 模型"四个维度逐层排查。每层都有健康检查接口与 Langfuse trace 入口，组合起来可在 5 分钟内定位 80% 问题。`

const PLAIN_SAMPLE = `知识库的检索质量受多个因素影响，最直接的是切分策略与嵌入模型的匹配度。切分过粗会导致单段语义混杂、相关度被稀释；切分过细则丢失上下文，单独检索某一段无法回答跨段问题。一般建议切分大小落在嵌入模型推荐窗口的 50%–80%，既保证语义完整又留出余量。常见嵌入模型如 BGE、Cohere 的 embed-v3 推荐窗口为 512 tokens，对应字符大致在 300–800 之间，因为中文一个字约 0.5–1.2 个 token，英文一个词约 1.3 个 token。

除了切分大小，重叠（overlap）也会显著影响召回完整性。重叠为 0 时，跨段问题往往只能召回半句话；重叠 10%–20% 通常就足够覆盖大多数边界情况；重叠超过 30% 会让相邻分块大量同质化，反而增加索引成本而不提升召回。一个简单的判断方法：对你的目标查询，平均答案长度的 1/3 作为 overlap 起点，再根据 A/B 测试微调。

分隔符的选择应贴合文档真实结构。纯文本可保留默认双换行作为强分隔；Markdown 文档建议保留标题层级，先按标题切，再在大标题内部按段落切；代码与表格混合的内容则需要更精细的策略，例如 Tree-sitter 抽取代码结构、识别表格边界，或者直接用脚本把这两类内容预先分离再走文本切分。如果文档里大量混合中英文，分隔符里务必同时包含中文标点（。！？）和英文标点（. ! ?），否则只对一种语言生效，效果会很差。

嵌入模型的选择往往被低估。开源里 BGE-large 系列在中文 benchmark 上稳定领先，BGE-M3 同时支持稠密、稀疏、ColBERT 多向量；商用 OpenAI text-embedding-3-large 通用性强、成本不算高，Voyage-3-large 在英文上略胜一筹但中文一般。对长文档场景，注意嵌入模型的最大输入长度——OpenAI 是 8191 tokens，BGE 默认 512，超长输入会被截断或分段嵌入再平均，效果都不好。

最后，别忽视后处理。重排（rerank）几乎在所有场景都能提升 5%–15% 的端到端效果，代价是每次查询多 100–300ms 延迟。常见 reranker 包括 BGE-Reranker、Cohere Rerank，前者开源、后者付费但效果略好。如果你的检索路径包含关键词召回，强烈建议用 reranker 做第二层过滤，不然 BM25 的纯字面命中会污染上下文。`

export const CHUNKING_SAMPLES: ChunkingSample[] = [
  { id: "markdown", labelKey: "samples.markdown", text: MARKDOWN_SAMPLE },
  { id: "faq", labelKey: "samples.faq", text: FAQ_SAMPLE },
  { id: "chapter", labelKey: "samples.chapter", text: CHAPTER_SAMPLE },
  { id: "plain", labelKey: "samples.plain", text: PLAIN_SAMPLE },
];

export const DEFAULT_SAMPLE_ID = "markdown";
