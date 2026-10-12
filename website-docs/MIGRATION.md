# 旧 docs 文档迁移记录

后续产品、部署、API 和开发文档统一维护在 `website-docs/`。本记录用于维护者审查迁移范围，不发布到文档站。本次删除 87 篇已迁移、重复或过时的旧手写文档，不保留第二套正文。旧内容可从 Git 历史追溯；`docs/` 仅保留下文列出的工程资源。

## 补入新站的内容

| 旧文档（相对于 docs） | 新入口 | 处理 |
| --- | --- | --- |
| `QA.md`、`migration-troubleshooting.md` | [常见问题与升级排障](01-getting-started/05-troubleshooting.md) | 保留现象定位和恢复流程，功能细节链接到现有章节；纠正“失败必然完整回滚”和机械 force 的说法 |
| `paradedb-upgrade.md` | [ParadeDB 升级](01-getting-started/06-paradedb-upgrade.md) | 保留备份、镜像与扩展 SQL 升级、回滚和验证入口；不把历史测试结果当成本次验证 |
| `sandbox-cluster.md`、`sandbox-docker-backend.md`、`sandbox-desktop.md`、`sandbox-protocol.md` | [沙箱部署与排障](06-development/04-sandbox-deployment.md) | 保留模板、daemon、网关、Redis、桌面及快照边界；说明旧 local 已移除、桌面 host 与服务端命名配置的区别，去除旧 Docker 实现对比和未核验的第三方部署规格 |
| `browser-skill-integration.md`、`browser-skill-production.md` | [本机浏览器](05-clients/09-local-browser.md) | 补配对、人工参与、任务恢复、配套构建、代理和多副本要求；与知识管理助手插件区分 |
| `agent-prompt-assembly.md` | [对话提示词拼装](06-development/05-agent-prompts.md) | 保留当前拼装入口、模板保存和消息边界，省略逐轮评审记录 |
| `mcp-tool-directory.md` | [MCP 集成](03-features/08-mcp.md#mcp-tool-directory)、[MCP API](04-api/02-api-agent-mcp.md) | 替换旧的全量注册说明，补持久目录、主体隔离、按需加载与同步接口；修正旧文档对 refresh 行为的矛盾说法 |
| `worker-pool-governance.md` | [异步任务容量](02-architecture/05-async-tasks.md#capacity-planning) | 沿用新站较新的队列拓扑，补聚合配置退役、容量估算和下游配额边界 |
| `embed-subdomain.md`、`embed-secure-mode.md` | [嵌入渠道](03-features/13-embed-channel.md#embed-subdomain) | 安全模式主体已覆盖，补独立子域、运行时地址与代理策略；不迁移只检查 Cookie/Header 是否存在的伪登录校验示例 |
| `wiki/集成扩展/飞书云盘数据源接入说明.md` | [飞书云盘接入](03-features/24-feishu-drive.md) | 补文件夹授权与解析模式；按当前实现修正默认 export、blocks 回退与图片条件、基于旧游标的删除检测和失败恢复边界 |
| `dev/opensearch-integration-test.md` | [检索引擎：本地联调](03-features/05-retrieval-engines.md#opensearch-local-testing) | 合入现有页面；补开发集群、SSRF 和验证流程，明确当前副本数 0 实际回退为 1；清理命令改为定向停止，避免删除其他开发数据卷 |
| `cloud-image/README.md`、`cloud-image/tencent-lighthouse.md` | [开发指南：云镜像脚本](06-development/01-dev-guide.md#cloud-image-scripts) | 只保留脚本边界与首启恢复，不另建部署教程；不沿用旧云平台配额、价格、审核时长和固定控制台路径 |

## 已由新站覆盖，不再复制全文

以下按主题比较；“覆盖”指有效功能和开发入口已有归属，不意味着保留所有旧示例、截图或逐函数讲解。

| 旧文档 | 维护位置 |
| --- | --- |
| `开发指南.md`、`快速开发模式说明.md` | [开发指南](06-development/01-dev-guide.md) |
| `LITE.md` | [安装部署](01-getting-started/02-installation.md)、[桌面客户端](05-clients/05-desktop.md)；发布包 README 仍有单独依赖，见下文 |
| `CHUNKING.md` | [分块](03-features/04-chunking.md) |
| `KnowledgeGraph.md`、`开启知识图谱功能.md` | [知识图谱](03-features/09-knowledge-graph.md) |
| `使用其他向量数据库.md` | [检索引擎](03-features/05-retrieval-engines.md)、[扩展点](06-development/03-extension-points.md) |
| `添加新的网络搜索引擎.md` | [联网搜索](03-features/11-web-search.md)、[扩展点](06-development/03-extension-points.md) |
| `数据源导入开发文档.md` | [数据源同步](03-features/10-datasource.md)、[扩展点](06-development/03-extension-points.md)；云盘实操另补新页 |
| `IM集成开发文档.md` | [IM 集成](03-features/12-im-integration.md)、[扩展点](06-development/03-extension-points.md)；旧平台后台截图/批量权限清单不直接复用 |
| `OIDC认证调用流程.md`、`RBAC说明.md`、`共享空间说明.md` | [认证与授权](03-features/01-tenant-auth.md)、[认证 API](04-api/02-api-auth.md)、[组织 API](04-api/02-api-org.md) |
| `BUILTIN_MODELS.md` | [模型管理](03-features/06-models.md)、[平台管理](03-features/20-platform-admin.md) |
| `BUILTIN_MCP_SERVICES.md`、`MCP功能使用说明.md`、`zh/mcp-approval.md` | [MCP 集成](03-features/08-mcp.md) |
| `Langfuse集成.md`、`日志配置.md` | [可观测性](03-features/16-observability.md)、[配置参考](01-getting-started/04-configuration.md) |
| `agent-skills.md`、`agent-tools-design.md` | [技能与沙箱](03-features/22-skills-sandbox.md)、[Agent 引擎](03-features/07-agent.md)、新增沙箱部署页；旧工具名、local 配置及过时的普通用户/只读镜像契约不迁移 |
| `chat-steering.md` | [会话体验](03-features/18-chat-experience.md)、[会话 API](04-api/02-api-chat.md) |
| `client-integration-upgrade-notes.md` | [CLI](05-clients/02-cli.md)、[Go SDK](05-clients/03-go-sdk.md)、[小程序](05-clients/04-miniprogram.md)、[文件访问](03-features/21-file-access.md) |
| `api/*.md` | [API 概览](04-api/01-api-overview.md)及同目录各主题；补缺失的 MCP metadata 与沙箱桌面接口，不再维护第二套手写 API |
| `wiki/` 中其他页面 | 上述主题的摘要副本，不整套迁移；其中旧导航、反向链接与图谱不进入新站 |

## 不作为当前产品文档迁移

- `ROADMAP.md`：旧计划包含已实现能力，不能当成当前承诺；入口改为现有产品介绍，计划应重新确认后再编写。
- `code-slimming-audit.md`：一次性代码精简审计，删除正文，仅通过 Git 历史保留。
- `plans/2026-09-10-faq-enabled-filter-design.md`：历史设计过程，删除正文；最终接口由 FAQ/API 章节承接，设计过程可查 Git 历史。
- `poc/docker-sandbox/`：独立 Go 模块和旧 Docker 可行性验证，不是用户文档；后续可移至开发实验目录，不混入站点。

## docs 目录保留的工程资源

1. **Swagger 生成包**：`internal/router/router.go` 导入 `github.com/Tencent/WeKnora/docs`；`Makefile` 的 `docs` 目标把输出写到该目录。`docs.go` 当前参与后端编译，`swagger_contract_test.go` 还读取 JSON/YAML。普通构建与 CI 未自动生成，暂时保留三份生成物及测试。未来可迁往独立生成包并更新导入、生成路径、测试和 lint 排除；若不提交生成物，必须先把固定版本的生成步骤接入所有构建、测试与发布入口。
2. **Lite 发布 README**：`scripts/package-lite.sh` 和 `.github/workflows/release-lite.yml` 复制 `docs/LITE.md` 到离线发布包；迁出时需保留适合离线阅读的 README，不能直接换成含站内相对链接的长篇页面。
3. **图片**：多语言 README、Helm 等仍引用 `docs/images/`、`docs/assets/`，本次保留。新站自身图片位于 `public/`，不依赖旧目录。
4. **历史实验**：保留 `poc/docker-sandbox/` 独立 Go 模块及运行说明；它不属于维护中的产品文档。旧手写正文（包括 API、Wiki 副本、路线图、审计和设计记录）已删除。

环境变量示例、前端帮助与分块示例、Helm 安装提示、示例项目、代码注释和 CHANGELOG 中的可点击文档链接均改指向新站。CHANGELOG 中描述旧版本曾新增哪些文件的历史文字保留原貌，不作为当前文档入口。`scripts/cloud-image/README.md` 收敛为新站入口，避免继续维护重复教程。

## 本次检查范围

迁移以当前仓库的路由、处理器、沙箱/浏览器实现、数据源服务、迁移 SQL 和部署脚本为依据。站点执行链接检查、Mermaid 检查和文档构建；未在此次文档整理中执行生产数据库升级、云镜像清理、真实沙箱集群或 IM 平台接入。

## 准确性与必要性复核

本次新增独立页面保留六类：通用排障、ParadeDB 存量升级、飞书云盘接入、本机浏览器、沙箱部署、提示词维护。它们分别补充跨章节排障、带状态升级、授权与解析选择、独立客户端部署、沙箱运维和开发契约；既有页面仅保留概要与链接。

- OpenSearch 联调并入检索引擎，云镜像维护并入开发指南，避免新增内容较少或尚未实测的独立教程。
- 提示词页按 `prompts.go`、`observe.go`、`finalize.go` 和 `config/agent_prompts.go` 核对；同步修正 Agent 旧章节的日期格式、MCP 提及范围与收尾消息角色，移除重复的浏览器内部协议说明。
- 飞书云盘按共享 `core/engine.go`、`core/shared.go` 与数据源服务核对；不再承诺全量同步可以补齐删除。第三方权限申请由官方接口说明承接，不固化未经本次平台验证的权限清单。
- OpenSearch 按驱动配置与索引实现核对，移除无效的零副本示例，并修正旧页的单索引维度说明。
- 数据库升级步骤区分生产与开发 Compose；沙箱文档区分桌面 host 与三种服务端命名配置；云镜像标明 systemd 固定路径与首启标记的实际写入时机。

以上属于源代码与配置核对；不将其等同于真实平台接入或生产升级验收。
