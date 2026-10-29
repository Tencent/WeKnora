# v0.9.0 发布审查记录

状态：文案、文档、demo 实测截图与发布材料已完成本地检查；视频已沿用获认可的原片风格扩展为 165 秒 HTML 宣传动画，于 2026-10-28 获用户确认。发布日期确定为 2026-10-29；正式 tag、镜像发布与官网部署尚未执行。上一轮 PR #4836 已合入，本轮将审查范围推进至 2026-10-29 获取的 upstream main，并补齐原基线之后的发布文档差异。

## 对比基线与历史依据

- 上次发布：v0.8.3，2026-10-14，`3e22822c1d2397be730c458c7539d0eb8c113a24`。
- 本次源码基线：`c73c06b4b0a95ecec5f3b180311a4451f29d5103`，分支 `chore/finalize-v0.9.0-release`，对应已合入的 PR #4836。清单截至此源码基线，不包含本轮收尾提交；最终 tag 应选择收尾 PR 合入后的 commit。
- 范围：**315 个提交，1929 个变更路径**；完整清单见[提交 TSV](release-v0.9.0-commits.tsv)与[文件 TSV](release-v0.9.0-files.tsv)。这是完整的提交/文件差异清单，不等同于对所有业务代码逐行进行安全审计。
- 风格：首页四项重点左文右图，四段说明展开；章节底色交替，浏览器展示保持独立；功能页先说明用途和步骤，再放字段/实现参考。README 每个发布主题直达明确锚点，历史版本页保持不变。

## 全量差异如何落实到材料

| 变更范围 | 本轮处理 | 主要代码依据 |
| --- | --- | --- |
| 助手与页面操作 | 独立指南、首页主视觉、版本说明 | `config/builtin_agents.yaml`、`internal/agent/tools/operations*`、`internal/productdocs`、`internal/types/assistant.go` |
| 多智能体协作 | 独立说明任务隔离、数量与并发、取消和权限 | `internal/agent/tools/collaboration.go`、协作服务和持久化模型 |
| 自动化与通知 | 用户指南、API、运行/时区/审批限制 | `internal/types/automation.go`、`internal/application/service/automation.go`、`internal/router/routes_automation.go` |
| 权限与 HITL | 重写过时的 MCP-only 审批，说明三级权限、授权与恢复边界 | `internal/agent/policy`、`internal/types/agent_grant.go`、prompts / suspensions |
| 沙箱与后台 Shell | 更新共享机器边界、技能更新、回滚与 Lite 审批 | `shell_exec_async.go`、`work.go`、沙箱分配及 host approval 实现 |
| 图像与解析 | 替换旧管线标识/轨迹；解释百分比字符串与 OCR 预处理 | `image_pipeline_default.go`、`image_pipeline_smartocr.go`、`knowledgebase.go`、Docreader |
| 数据源与检索 | 保留当前已更新的飞书/PAT/图谱 API/MCP 文档过滤，精简目录说明，补 RRF 入口 | datasource connectors、retrievers、router 与 MCP tools |
| 部署与数据迁移 | 逐版纳入新增迁移；列出重同步、空图片分块、共享沙箱开关和配额变化 | `migrations/versioned/000119–000134`、`migrations/sqlite/000037–000051`、`.env.example` |
| 其余模型、IM、CLI、SDK、UI、修复与测试 | 全部保留于提交清单；按影响汇总至 CHANGELOG 与版本页 | 全量 Git 对比；不把修复项都提升成首页卖点 |

## 2026-10-29 发布收尾

原基线 `256a3f2966866337f7dc3c5fcc10037f2ae18d52` 之后新增 14 个提交，已按功能核对代码并纳入发布材料：

| 增量范围 | 发布材料处理 |
| --- | --- |
| #4836 发布素材与助手入口引导 | 保留已确认的视频、截图和首页；在助手指南与版本说明补充一次性入口引导 |
| #4822 空间默认设置 | 补充默认模型与搜索服务说明、完整 API 语义、PG 000134 / SQLite 000051 迁移及升级提示 |
| #4824 持久化审批等待 | 说明持久化审批显示等待及到期时间，不使用短时倒计时 |
| DSML 提取与校验（2 个提交） | CHANGELOG 与版本说明补充已声明工具及参数校验 |
| Markdown 空白、MCP 完成事件、WeKnoraCloud 响应限制（3 个提交） | 补充内容保真、异常流与响应大小处理 |
| 最终回答汇总、Ollama 结束原因（2 个提交） | 补充流式完成状态与思考参数修复 |
| BLEU、NDCG、SQLite 空查询（3 个提交） | 补充评估与检索修复，并更新评估指标说明 |
| 测试格式整理（1 个提交） | 纳入完整提交清单，无用户可见行为变化 |

四种语言 README、CHANGELOG 与版本页统一使用 2026-10-29；对比快照保留在本审查记录，用户版本页不再带过时的 2026-10-27 快照说明。通知正文已补齐，可在 tag、镜像和官网验收后使用。

## 文档处置清单

范围包括下列 74 篇编号文档，并连同网站说明、导航、四种语言 README、CHANGELOG、版本元数据和新增审查材料一起维护。保留项仍参与构建与链接校验；历史说明不会为了当前版本重写。

| 页面 | 处置 | 说明 |
| --- | --- | --- |
| [WeKnora 产品介绍](../website-docs/01-getting-started/01-introduction.md) | 更新 / 重写部分章节 | 补充四项能力入口 |
| [安装部署](../website-docs/01-getting-started/02-installation.md) | 更新 / 重写部分章节 | 更新目标版本示例 |
| [快速上手](../website-docs/01-getting-started/03-quickstart.md) | 更新 / 重写部分章节 | 加入助手只读帮助入口 |
| [配置详解](../website-docs/01-getting-started/04-configuration.md) | 更新 / 重写部分章节 | 新增预处理、表格、沙箱开关和问题生成预算 |
| [常见问题与升级排障](../website-docs/01-getting-started/05-troubleshooting.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [ParadeDB 存量库升级](../website-docs/01-getting-started/06-paradedb-upgrade.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [总体架构](../website-docs/02-architecture/01-overview.md) | 更新 / 重写部分章节 | 补充助手与持续任务架构 |
| [Go 后端设计](../website-docs/02-architecture/02-backend-design.md) | 更新 / 重写部分章节 | 补充权限、审批、协作及自动化模块 |
| [文档入库流程（Document Ingestion Pipeline）](../website-docs/02-architecture/03-document-pipeline.md) | 更新 / 重写部分章节 | 更新可配置图片管线 |
| [检索问答全流程（RAG Pipeline）](../website-docs/02-architecture/04-rag-pipeline.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [异步任务系统](../website-docs/02-architecture/05-async-tasks.md) | 更新 / 重写部分章节 | 区分入库队列与自动化/后台任务 |
| [租户、用户与认证授权](../website-docs/03-features/01-tenant-auth.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [知识库与知识管理](../website-docs/03-features/02-knowledge-base.md) | 更新 / 重写部分章节 | 替换旧图片管线和轨迹字段说明 |
| [文档解析服务 docreader](../website-docs/03-features/03-document-parsing.md) | 更新 / 重写部分章节 | 重写图片流程与百分比输出说明 |
| [分块机制（Chunking）](../website-docs/03-features/04-chunking.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [检索引擎与向量存储（Retrieval Engines）](../website-docs/03-features/05-retrieval-engines.md) | 更新 / 重写部分章节 | 加入 RRF 用户操作说明 |
| [模型管理](../website-docs/03-features/06-models.md) | 更新 | 补齐空间默认模型入口、用途、回退规则与默认搜索服务 |
| [Agent 引擎](../website-docs/03-features/07-agent.md) | 更新 / 重写部分章节 | 新增协作入口及权限字段，重写旧 MCP-only 内存审批说明 |
| [MCP（Model Context Protocol）集成](../website-docs/03-features/08-mcp.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [知识图谱](../website-docs/03-features/09-knowledge-graph.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [数据源导入（Data Source）](../website-docs/03-features/10-datasource.md) | 更新 / 重写部分章节 | 精简钉钉目录说明，保留已补齐的 Feishu/PAT 契约 |
| [网络搜索与网页抓取](../website-docs/03-features/11-web-search.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [IM 集成（IM Integration）](../website-docs/03-features/12-im-integration.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [网页嵌入（Embed Channel）](../website-docs/03-features/13-embed-channel.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [Wiki 能力](../website-docs/03-features/14-wiki.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [评估能力（Evaluation）](../website-docs/03-features/15-evaluation.md) | 更新 | 说明 BLEU 零精度与 NDCG 理想排序修复，提示重新运行比较基线 |
| [可观测性与审计](../website-docs/03-features/16-observability.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [FAQ 能力](../website-docs/03-features/17-faq.md) | 更新 / 重写部分章节 | 补充列表展开方式 |
| [会话与对话体验](../website-docs/03-features/18-chat-experience.md) | 更新 / 重写部分章节 | 补充助手、协作和后台任务进度 |
| [存储后端（Storage Backends）](../website-docs/03-features/19-storage-backends.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [平台管理与系统管理员](../website-docs/03-features/20-platform-admin.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [图片与文件的对外访问](../website-docs/03-features/21-file-access.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [技能目录与沙箱](../website-docs/03-features/22-skills-sandbox.md) | 更新 / 重写部分章节 | 更新共享边界、路径、回滚和后台 Shell，补本机审批 |
| [跨会话长期记忆](../website-docs/03-features/23-memory.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [飞书云盘接入](../website-docs/03-features/24-feishu-drive.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [Seafile 接入](../website-docs/03-features/25-seafile.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [WeKnora 助手](../website-docs/03-features/26-weknora-assistant.md) | 新增 | 新建完整助手与协作指南 |
| [自动化](../website-docs/03-features/27-automations.md) | 新增 | 新建自动化任务指南 |
| [执行权限与审批](../website-docs/03-features/28-agent-permissions.md) | 新增 | 新建执行权限与授权指南 |
| [API 总览](../website-docs/04-api/01-api-overview.md) | 更新 / 重写部分章节 | 加入自动化 API 与认证边界 |
| [API 参考：Agent、MCP 与技能](../website-docs/04-api/02-api-agent-mcp.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [API 参考：认证与用户](../website-docs/04-api/02-api-auth.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [API 参考：自动化与通知](../website-docs/04-api/02-api-automations.md) | 新增 | 新建自动化、记录、授权和通知 API |
| [API 参考：IM、Embed 与文件服务](../website-docs/04-api/02-api-channels.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [API 参考：会话、消息与聊天](../website-docs/04-api/02-api-chat.md) | 更新 / 重写部分章节 | 补充助手请求字段、后台任务接口 |
| [API 参考：分块与标签](../website-docs/04-api/02-api-chunks.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [API 参考：FAQ 与 Wiki](../website-docs/04-api/02-api-faq-wiki.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [API 参考：基础设施与数据源](../website-docs/04-api/02-api-infra.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [API 参考：知识库与知识](../website-docs/04-api/02-api-knowledge.md) | 更新 / 重写部分章节 | 补充图像管线选择与校验接口 |
| [API 参考：长期记忆](../website-docs/04-api/02-api-memory.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [API 参考：模型与初始化](../website-docs/04-api/02-api-model-system.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [API 参考：组织与共享](../website-docs/04-api/02-api-org.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [API 参考：沙箱、技能与个人变量](../website-docs/04-api/02-api-sandbox-skills.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [API 参考：系统与平台管理](../website-docs/04-api/02-api-system.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [API 参考：租户（空间）与成员](../website-docs/04-api/02-api-tenant.md) | 更新 | 补齐默认设置的全量替换、校验、响应和用途标记语义 |
| [Web 前端（frontend/）](../website-docs/05-clients/01-frontend.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [WeKnora CLI（weknora 命令行工具）](../website-docs/05-clients/02-cli.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [Go SDK](../website-docs/05-clients/03-go-sdk.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [微信小程序客户端](../website-docs/05-clients/04-miniprogram.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [桌面客户端（WeKnora Lite Desktop）](../website-docs/05-clients/05-desktop.md) | 更新 / 重写部分章节 | 区分 Lite 本机审批与全局偏好模式 |
| [Chrome 插件（知识管理助手）](../website-docs/05-clients/06-chrome-extension.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [Claw Skill](../website-docs/05-clients/07-claw-skill.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [DeepSeek Harness 插件](../website-docs/05-clients/08-deepseek-harness.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [本机浏览器接入与部署](../website-docs/05-clients/09-local-browser.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [开发指南](../website-docs/06-development/01-dev-guide.md) | 保留已有更新 | 本发布周期已有补充；纳入链接、构建和关联变更核对 |
| [数据库与迁移](../website-docs/06-development/02-database-schema.md) | 更新 / 重写部分章节 | 更新 PG 119-134 / SQLite 37-51 迁移与空间默认模型字段 |
| [扩展点指南](../website-docs/06-development/03-extension-points.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [沙箱部署与排障](../website-docs/06-development/04-sandbox-deployment.md) | 更新 / 重写部分章节 | 修正共享机器隔离与本机审批边界 |
| [对话提示词拼装与可编辑范围](../website-docs/06-development/05-agent-prompts.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [WeMM Transformers 部署](../website-docs/06-development/06-wemm-transformers.md) | 保留 | 未发现需要本轮迁移的内容；保留并纳入全站校验 |
| [v0.8.0 版本说明](../website-docs/07-releases/v0.8.0.md) | 保留 | 历史版本记录，保持原发布事实与锚点 |
| [v0.8.2 版本说明](../website-docs/07-releases/v0.8.2.md) | 保留 | 历史版本记录，保持原发布事实与锚点 |
| [v0.8.3 版本说明](../website-docs/07-releases/v0.8.3.md) | 保留 | 历史版本记录，保持原发布事实与锚点 |
| [v0.9.0 版本说明](../website-docs/07-releases/v0.9.0.md) | 新增 | 新建版本说明、视频和升级要求 |

## 删除与重写的内容

本次没有整篇删除仍有有效用途的文档。删除了当前指南中的过时结论与重复实现细节：仅 MCP 需要审批、全部审批只保存在内存、旧图片管线/轨迹字段、共享分配为零就可无损降级，以及冗长的钉钉路径内部实现叙述。修正助手聊天请求字段为 `assistant_access`，普通智能体配置字段为 `config.access_level`，避免混用自动化的 `access_level`。

## 文案修订

版本说明围绕「WeKnora 工作助手」介绍用途；首页重点章节标题调整为「WeKnora 助手、多智能体协作与自动化」。四项重点为「了解 WeKnora，也帮你处理任务」「多个智能体同时处理任务」「一句话安排，也能手动设置」「决定哪些操作需要确认」。首项同时说明产品问答、页面上下文和当前用户身份；助手与自动化各使用两张可切换的截图。首页、指南、版本说明、四种语言 README、CHANGELOG 和通知正文已同步；视频开场使用英文「Your WeKnora assistant.」，结尾使用「Ask. Create. Work together.」。

首页将 v0.8.3 的长期能力归回对应章节：多模态检索与原文定位归入 RAG，附件按需读取归入 Agent，Wiki 图谱与历史保留在 Wiki，Docker 终端与本机技能保留在技能与沙箱，目录同步归入数据源，平台用户管理归入企业管理。按用户后续要求，删除新增的「模型接入与管理」段落，保留原有厂商展示。移除顶部跨主题的「继续了解」链接，四项 v0.9.0 重点独立保留。画廊作为知识库的浏览方式单独展示，放在 Wiki 前；按用户要求沿用上一版两张画廊截图，未替换素材。


## 截图与视频来源

六张截图使用 demo 账号及「周末读书会」虚构资料，尺寸为 1920 × 1056，与旧版保持 20:11；文档站和首页副本一致。助手展示产品问答与任务协助，自动化展示一句话设置与手动设置，协作和审批使用卡片视图。旧版画廊图片保持不变。输入资料和实测范围见[演示资料](../misc/release-v0.9.0-demo/README.md)。

视频沿用仓库 `misc/intro-video/` 的衬线标题、纸白背景、深蓝金色、流式文字和平滑转场，按当前产品源码重建各功能。全部界面由 HTML/CSS/SVG 编排，只有官方 Logo 使用位图。片中资源与操作是虚构的说明性示例，不是实际运行或性能证据。

成片为 165 秒、1920 × 1080、60 fps、H.264 / AAC，包含 28 段英文合成配音、34 条内嵌英文字幕及独立 SRT，无背景音乐；715 个字符的打字音效与输入同步，并在人声下自动降低音量。封面展示四项重点能力，随后覆盖产品问答、创建智能体、具名协作、自动化、权限审批、后台产物、Browser Skill、画廊、Wiki Graph 与原文引用。创建从页面弹窗进入助手；审批后展示知识库卡片名称变化；浏览器浮窗同步页面、焦点与光标。源码、分镜与复现步骤见[视频工程](../misc/release-v0.9.0-video/README.md)。

## 验证与边界

- 上一轮演示账号已验证资料解析、助手来源引用、智能体创建、三个子任务协作、自动化手动运行、只读写入拒绝、单次审批后创建及改名。自动化保持暂停。测试环境并非固定发布构建；模型重复调用和缺少工具参数后的重试路径仍需在最终构建复测。
- 2026-10-29 重跑网站完整构建通过：84 页、412 个本地路由/资源、83 个首页资源；81 篇文档的 407 个内部链接失效 0 个，60 个 Mermaid 图失败 0 个。首页 ESLint、`go test ./internal/productdocs` 和差异空白检查通过。站点集成测试已更新为 v0.9.0 助手入口，导航、主题、搜索、移动端、文档直达与 404 检查通过。首页视频默认展示 v0.9.0，旧产品全览通过紧凑横向标签保留，共用一个播放器；顶部合并为单行工具栏，移除重复标题；播放、切换后停止旧视频、键盘选择、移动端并排及点击前不加载媒体的检查通过。构建仍有 Vite 大分块提示。
- 沿用 2026-10-28 已确认成片的检查结果：覆盖 660 个采样时刻、11 个输入区的输入与提交状态、715 个字符的逐帧到达、字幕安全区、审批边界和浏览器浮窗同步，无布局错误。全片解码通过，共 9900 帧；音频为 48 kHz 双声道，-18.04 LUFS、-4.95 dBTP，无削波。
- 最终 MP4 为 24,185,842 字节，SHA-256：`d38750449839f6a3d99b75dd0373e531cd5c2d1e4108bf8ed454b617e9d2317f`。网站副本和本地发布包内容一致。
- 未执行生产迁移、镜像发布、线上部署或生产冒烟测试。本记录的差异范围固定于上述基线；后续提交需要追加审查。

## 交付物

- [发布说明](../website-docs/07-releases/v0.9.0.md)与[通知正文](release-v0.9.0-announcement.md)。
- [六张截图](../website-docs/public/screenshots/)及虚构演示资料。
- [英文动画 MP4](../website-docs/public/videos/weknora-v0.9.0-en.mp4)、[封面](../website-docs/public/videos/weknora-v0.9.0-poster.png)、[英文字幕](../website-docs/public/videos/weknora-v0.9.0-en.srt)及可编辑动画源码。
- 静态站和媒体包通过网站 README 中的打包流程生成；`website-docs/releases/` 为忽略的本地构建目录。

## 正式发布前的最后步骤

1. 合入本轮收尾 PR，确定最终 tag commit。发布日期已统一为 2026-10-29；如 main 继续变化，对上述源码基线之后的提交追加核对。
2. 在固定发布构建验收数据库迁移、解析/检索、助手权限、审批恢复、自动化通知、空间默认模型与所选沙箱分配模式。
3. 发布 tag 和各平台产物，确认 0.9.0 镜像可用。
4. 按网站 README 部署静态站，检查版本页、视频播放与范围请求，以及 README 和历史链接。
5. 根据通知正文发布 Release 与版本通知。

公开提交不包含账号标识、私有会话记录、模型缓存或废弃视频草稿。正式发布与网站部署仍待执行。
