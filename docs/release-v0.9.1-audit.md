# v0.9.1 发布核对记录

准备日期为 2026-11-23。上一正式版是 2026-10-29 发布的 v0.9.0。正式发布日期以 GitHub Release 为准。

## 版本范围

- 起点为 v0.9.0 的 `f17ee8a8da901bd658a4cb25f40b5fd41adbfdd7`，终点为上游 main 的 `032c67f30d63f744ffa9131887c35f7d15121224`。[固定范围对比](https://github.com/Tencent/WeKnora/compare/v0.9.0...032c67f30d63f744ffa9131887c35f7d15121224)
- 这个范围有 **378 个提交，1,381 个变化路径**，其中新增 551 个、修改 829 个、删除 1 个。提交日期按提交者日期记录。两份 TSV 收录全部差异，本轮发布文档和元数据改动在该范围之后。[提交清单](release-v0.9.1-commits.tsv) · [文件清单](release-v0.9.1-files.tsv)
- 上次发版的图文、通知、历史视频和材料分工沿用自 [v0.9.0 发布记录](release-v0.9.0-audit.md)。本次按图文发布，网站和四种语言的 README 均保留 v0.9.0 英文视频及其真实版本标识。

## 本次公开材料

| 使用场景 | 文件 |
| --- | --- |
| 官网图文、升级与回退 | [v0.9.1 版本说明](../website-docs/07-releases/v0.9.1.md) |
| 给用户群、GitHub Release、社区跟帖 | [公告正文](release-v0.9.1-announcement.md) |
| 英文变化记录 | [CHANGELOG](../CHANGELOG.md) |
| 首页四个产品场景 | [官网首页](../website-docs/homepage/app/page.tsx) |
| README 入口 | [中文](../README_CN.md) · [English](../README.md) · [日本語](../README_JA.md) · [한국어](../README_KO.md) |
| 独立官网包和部署步骤 | 运行 `npm run package:site` 得到 `website-docs/releases/weknora-site-v0.9.1.tar.gz`。[部署指南](../website-docs/README.md) |

## 变更与公开说法的依据

| 内容 | 这次交付的具体变化 | 已合入的依据 |
| --- | --- | --- |
| 图像与搜索 | 编辑已有 OCR 和说明，按 PDF 原页排列，通过 API 以图查询，搜索命中定位到原文，引用显示知识库 | [#4942](https://github.com/Tencent/WeKnora/pull/4942) · [#4649](https://github.com/Tencent/WeKnora/pull/4649) · [#4823](https://github.com/Tencent/WeKnora/pull/4823) · [#5177](https://github.com/Tencent/WeKnora/pull/5177) · [#5343](https://github.com/Tencent/WeKnora/pull/5343) |
| 桌面与登录 | 支持会话隔离桌面的沙箱中，同一会话的桌面和智能体共用浏览器。个人凭据保险库和安全表单负责处理登录信息 | [#4785](https://github.com/Tencent/WeKnora/pull/4785) · [#5035](https://github.com/Tencent/WeKnora/pull/5035) · [#5047](https://github.com/Tencent/WeKnora/pull/5047) |
| 知识库与 Wiki | 重新解析保留旧索引与 Wiki 来源，支持关闭自动生成，问题生成模型可单独配置 | [#5024](https://github.com/Tencent/WeKnora/pull/5024) · [#5186](https://github.com/Tencent/WeKnora/pull/5186) · [#4707](https://github.com/Tencent/WeKnora/pull/4707) · [#4718](https://github.com/Tencent/WeKnora/pull/4718) |
| 数据源 | 新增 Nextcloud 的 WebDAV 同步，Confluence 处理附件与表格，Notion 改善嵌套内容读取 | [#4601](https://github.com/Tencent/WeKnora/pull/4601) · [#4953](https://github.com/Tencent/WeKnora/pull/4953) · [#4795](https://github.com/Tencent/WeKnora/pull/4795) · [#5042](https://github.com/Tencent/WeKnora/pull/5042) |
| 语音、文件选择与访客 | 聊天按住说话，提及文件可按目录浏览。嵌入访客能在当前浏览器续聊，签名访客可删除自己的会话 | [#4804](https://github.com/Tencent/WeKnora/pull/4804) · [#4700](https://github.com/Tencent/WeKnora/pull/4700) · [#4631](https://github.com/Tencent/WeKnora/pull/4631) · [#5715](https://github.com/Tencent/WeKnora/pull/5715) |
| 运维、部署与官网 | 文档进度、连接池、Helm Pod 注解、子路径部署、构建版本展示、社区案例 | [#4732](https://github.com/Tencent/WeKnora/pull/4732) · [#4349](https://github.com/Tencent/WeKnora/pull/4349) · [#5159](https://github.com/Tencent/WeKnora/pull/5159) · [#4977](https://github.com/Tencent/WeKnora/pull/4977) · [#4769](https://github.com/Tencent/WeKnora/pull/4769) · [#4902](https://github.com/Tencent/WeKnora/pull/4902) |

搜索 API 的 `query_image` 和画廊编辑分别写入各自的入口。已有的多模态索引、画廊、工作助手、多智能体协作与自动化仍可从原功能页和历史版本页访问。

## 配图与视频

四张 PNG 均来自候选版的真实 Vue 前端，通过隔离浏览器访问本地构建。演示账号、统计图、域名和目录使用虚构的「周末读书会」资料。接口返回由本地示例提供，截图没有使用真实账号、密码或生产数据。每张原图为 **1920 × 1056**，官网和文档引用的同名文件逐字节一致。

- [画廊 OCR 编辑](../website-docs/public/screenshots/release-v091-gallery.png)展示读书会的三个月统计图，并在真实编辑控件中校对文字。
- [个人凭据保险库](../website-docs/public/screenshots/release-v091-vault.png)展示示例账号及允许的域名。
- [Wiki 手动维护](../website-docs/public/screenshots/release-v091-wiki.png)同时呈现 Wiki 已启用、自动生成已关闭的设置。
- [Nextcloud 选目录](../website-docs/public/screenshots/release-v091-nextcloud.png)展示真实目录选择器和虚构的同步范围。

视频区继续提供一个播放器，默认 v0.9.0 英文片，可切换原有产品全览。v0.9.0 版本页、成片、字幕和封面继续使用原路径。

## 升级和发版前的事项

| 事项 | 当前记录和发布动作 |
| --- | --- |
| 发布日期与最终 SHA | 当前版本文件和文案按 v0.9.1 准备。收尾改动合入后，在最终 SHA 核对相对以上基线的新增改动与相应 CI，将 CHANGELOG 的 `Unreleased` 换为实际发布日期。 |
| 首个系统管理员 | 继续通过 `WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL` 指定邮箱。已有管理员的实例按正常流程升级。对外新部署须先在受控入口由负责人完成首个账号注册，再开放公众访问。公开注册下对首个管理员身份归属的处置，在打标前由维护者确认。[配置指南](../website-docs/03-features/20-platform-admin.md) · [#5712](https://github.com/Tencent/WeKnora/pull/5712) |
| Brotli 依赖 | 当前候选服务端使用 `github.com/andybalholm/brotli v1.2.0`。拟升级至 1.2.8 的 PR 在准备时仍未合入。正式发布前需处理此项，再核对最终二进制依赖。[依赖 PR #5701](https://github.com/Tencent/WeKnora/pull/5701) · [上游公告](https://github.com/andybalholm/brotli/security/advisories/GHSA-c9gr-gmg4-2vqv) |
| 数据库 | 上一轮用隔离 PostgreSQL 与 SQLite 分别从 134 升至 137、从 51 升至 53。第二次运行均为 clean，四类原有测试记录均保留。新增 IM 线程后旧约束可能阻止降级，凭据表与超长 PostgreSQL 审计标识也受向下迁移影响。用户步骤已写入[升级与回退](../website-docs/07-releases/v0.9.1.md#upgrade-notes)。 |
| 真实集成验收 | 本次图文用虚构的数据源与账号。上文的测试覆盖本地逻辑，封版时用测试租户核对实际登录接力、Nextcloud 与 Confluence 同步和配置的 ASR 供应商。 |
| 制品与对外发布 | 打标后核对 `v0.9.1` 的 app、ui、docreader、sandbox 镜像及所支持架构，再部署官网并打开版本页和旧视频。最后使用公告稿。Lite 桌面安装包走单独的手工工作流，公告按实际出包情况列下载项。 |

## 本轮本地验收

| 检查 | 结果 |
| --- | --- |
| 前端 | Node 24 下类型检查与生产构建通过。单测 2,891 项通过，1 项跳过。 |
| 官网与嵌入文档 | 检查 83 篇 Markdown 的 423 个内部链接、60 幅 Mermaid 图。静态站 86 页、439 个本地路由或资源检查通过。助手内置文档解析和引用锚点测试通过。 |
| 首页与历史影片 | 浏览器集成测试通过，覆盖发布区导航、旧影片播放与切换、同标签页返回、移动端菜单。静态包内原片与仓库中的 v0.9.0 视频校验值相同。 |
| 版本与部署 | 核对应用与官网 VERSION、两个 npm 包及其锁文件、四种语言 README 链接。Helm 3.19.0 lint 和 12 项 chart 测试通过，默认三个应用镜像均为 `v0.9.1`，显式标签可覆盖。Compose 渲染出 app、ui、docreader、sandbox 的同版镜像名。镜像发布后还需实际拉取。 |
| 重点能力 | Nextcloud 增量同步与断点恢复、个人凭据的维护、图片检索请求解析、Wiki 自动生成开关的 Go 回归用例通过，模型厂商注册表测试通过。 |
| 交付包 | 检查静态 tar.gz 的新旧发布页、原视频、四张图及对应首页副本。检查公告中的版本链接与 6 个公开锚点，逐行核对两份 TSV 与 Git 输出一致。 |

## 文档核对范围

上游区间内有 30 篇编号指南发生变化。本次同时核对安装指南、沙箱指南、数据库结构、历史版本目录和 README 等入口。编辑范围包括对已有文档补齐的字段和操作步骤。其余编号页按主题列在下表，均参加整站构建和链接检查。工程文档继续保留各自的用途，包括 [子路径部署](subpath-deployment.md)、[Confluence 表格设计](design/confluence-table-markdown.md) 与[网站迁移记录](../website-docs/MIGRATION.md)。

共 76 篇编号文档。工程维护页和首页不计入这一数字。

| 页面 | 本轮处理 | 依据 |
| --- | --- | --- |
| [WeKnora 产品介绍](../website-docs/01-getting-started/01-introduction.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [安装部署](../website-docs/01-getting-started/02-installation.md) | 本轮更新 | 校正 Compose 带 v 的镜像标签及 Helm 默认应用版本。 |
| [快速上手](../website-docs/01-getting-started/03-quickstart.md) | 本轮更新 | 明确首个管理员由部署负责人在受控入口完成注册。 |
| [配置详解](../website-docs/01-getting-started/04-configuration.md) | 沿用本版已合入的修订 | 保留数据库连接池、ASR 和首次管理员邮箱的配置说明。 |
| [常见问题与升级排障](../website-docs/01-getting-started/05-troubleshooting.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [ParadeDB 存量库升级](../website-docs/01-getting-started/06-paradedb-upgrade.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [总体架构](../website-docs/02-architecture/01-overview.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [Go 后端设计](../website-docs/02-architecture/02-backend-design.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [文档入库流程（Document Ingestion Pipeline）](../website-docs/02-architecture/03-document-pipeline.md) | 沿用本版已合入的修订 | 保留 DOCX 内含矢量图片时选择解析器的条件。 |
| [检索问答全流程（RAG Pipeline）](../website-docs/02-architecture/04-rag-pipeline.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [异步任务系统](../website-docs/02-architecture/05-async-tasks.md) | 沿用本版已合入的修订 | 保留 Lite 的异步任务进度缓存说明。 |
| [租户、用户与认证授权](../website-docs/03-features/01-tenant-auth.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [知识库与知识管理](../website-docs/03-features/02-knowledge-base.md) | 本轮更新 | 补充重解析保留旧索引、画廊编辑和 PDF 页码排序。 |
| [文档解析服务 docreader](../website-docs/03-features/03-document-parsing.md) | 沿用本版已合入的修订 | 保留 MinerU 图片预算、格式及解析说明。 |
| [分块机制（Chunking）](../website-docs/03-features/04-chunking.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [检索引擎与向量存储（Retrieval Engines）](../website-docs/03-features/05-retrieval-engines.md) | 沿用本版已合入的修订 | 保留按向量维度创建 HNSW 索引的条件。 |
| [模型管理](../website-docs/03-features/06-models.md) | 沿用本版已合入的修订 | 保留 31 个厂商及 Runcrate 说明，首页与四种 README 的数量同步。 |
| [Agent 引擎](../website-docs/03-features/07-agent.md) | 沿用本版已合入的修订 | 保留联网搜索工具的地区和数量参数。 |
| [MCP（Model Context Protocol）集成](../website-docs/03-features/08-mcp.md) | 沿用本版已合入的修订 | 保留远端 MCP 配置、内置 MCP Server 检索及重排边界。 |
| [知识图谱](../website-docs/03-features/09-knowledge-graph.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [数据源导入（Data Source）](../website-docs/03-features/10-datasource.md) | 沿用本版已合入的修订 | 保留 Nextcloud 与 Confluence 的连接、范围和同步规则。 |
| [网络搜索与网页抓取](../website-docs/03-features/11-web-search.md) | 沿用本版已合入的修订 | 保留 DeepSeek 与 SearchApi 的配置说明。 |
| [IM 集成（IM Integration）](../website-docs/03-features/12-im-integration.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [网页嵌入（Embed Channel）](../website-docs/03-features/13-embed-channel.md) | 沿用本版已合入的修订 | 保留当前浏览器历史、渠道和访客边界。 |
| [Wiki 能力](../website-docs/03-features/14-wiki.md) | 沿用本版已合入的修订 | 保留既有手动模式指南，并从版本页与首页链接。 |
| [评估能力（Evaluation）](../website-docs/03-features/15-evaluation.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [可观测性与审计](../website-docs/03-features/16-observability.md) | 沿用本版已合入的修订 | 保留 Langfuse 自定义元数据请求头说明。 |
| [FAQ 能力](../website-docs/03-features/17-faq.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [会话与对话体验](../website-docs/03-features/18-chat-experience.md) | 沿用本版已合入的修订 | 保留文件提及的目录浏览和按住说话说明。 |
| [存储后端（Storage Backends）](../website-docs/03-features/19-storage-backends.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [平台管理与系统管理员](../website-docs/03-features/20-platform-admin.md) | 本轮更新 | 补齐首次管理员的受控注册顺序与后续管理入口。 |
| [图片与文件的对外访问](../website-docs/03-features/21-file-access.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [技能目录与沙箱](../website-docs/03-features/22-skills-sandbox.md) | 本轮更新 | 补充同一会话的浏览器登录接力、凭据保险库与密钥要求。 |
| [跨会话长期记忆](../website-docs/03-features/23-memory.md) | 沿用本版已合入的修订 | 保留无账号身份的空间管理员管理说明。 |
| [飞书云盘接入](../website-docs/03-features/24-feishu-drive.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [Seafile 接入](../website-docs/03-features/25-seafile.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [Nextcloud 数据源](../website-docs/03-features/26-nextcloud.md) | 本轮更新 | 将新增的独立指南整理为中文，保留英文锚点、配置边界、同步语义和测试入口。 |
| [WeKnora 助手](../website-docs/03-features/26-weknora-assistant.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [自动化](../website-docs/03-features/27-automations.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [执行权限与审批](../website-docs/03-features/28-agent-permissions.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 总览](../website-docs/04-api/01-api-overview.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：Agent、MCP 与技能](../website-docs/04-api/02-api-agent-mcp.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：认证与用户](../website-docs/04-api/02-api-auth.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：自动化与通知](../website-docs/04-api/02-api-automations.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：IM、Embed 与文件服务](../website-docs/04-api/02-api-channels.md) | 沿用本版已合入的修订 | 保留签名嵌入访客删除自己会话的接口条件。 |
| [API 参考：会话、消息与聊天](../website-docs/04-api/02-api-chat.md) | 本轮更新 | 把知识检索的 query 改为有条件必填，补 query_image、输入格式、大小和图文分工。 |
| [API 参考：分块与标签](../website-docs/04-api/02-api-chunks.md) | 沿用本版已合入的修订 | 保留图片 OCR、描述的版本检查和索引更新规则。 |
| [API 参考：FAQ 与 Wiki](../website-docs/04-api/02-api-faq-wiki.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：基础设施与数据源](../website-docs/04-api/02-api-infra.md) | 沿用本版已合入的修订 | 保留新增联网搜索提供方和配置说明。 |
| [API 参考：知识库与知识](../website-docs/04-api/02-api-knowledge.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：长期记忆](../website-docs/04-api/02-api-memory.md) | 沿用本版已合入的修订 | 保留无账号身份管理接口。 |
| [API 参考：模型与初始化](../website-docs/04-api/02-api-model-system.md) | 沿用本版已合入的修订 | 保留更新模型配置时字段的省略与保留语义。 |
| [API 参考：组织与共享](../website-docs/04-api/02-api-org.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：沙箱、技能与个人变量](../website-docs/04-api/02-api-sandbox-skills.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [API 参考：系统与平台管理](../website-docs/04-api/02-api-system.md) | 沿用本版已合入的修订 | 保留运行时资料进度和 Wiki 批量启动条件。 |
| [API 参考：租户（空间）与成员](../website-docs/04-api/02-api-tenant.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [Web 前端（frontend/）](../website-docs/05-clients/01-frontend.md) | 沿用本版已合入的修订 | 保留主应用与嵌入页各自的国际化说明。 |
| [WeKnora CLI（weknora 命令行工具）](../website-docs/05-clients/02-cli.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [Go SDK](../website-docs/05-clients/03-go-sdk.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [微信小程序客户端](../website-docs/05-clients/04-miniprogram.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [桌面客户端（WeKnora Lite Desktop）](../website-docs/05-clients/05-desktop.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [Chrome 插件（知识管理助手）](../website-docs/05-clients/06-chrome-extension.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [Claw Skill](../website-docs/05-clients/07-claw-skill.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [DeepSeek Harness 插件](../website-docs/05-clients/08-deepseek-harness.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [本机浏览器接入与部署](../website-docs/05-clients/09-local-browser.md) | 沿用本版已合入的修订 | 保留 BrowserSkill 扩展版本要求。 |
| [开发指南](../website-docs/06-development/01-dev-guide.md) | 沿用本版已合入的修订 | 保留 Swagger 生成工具和文档契约检查入口。 |
| [数据库与迁移](../website-docs/06-development/02-database-schema.md) | 本轮更新 | 按 SQL 补齐 PostgreSQL 135–137、SQLite 52–53 及两种库的差异。 |
| [扩展点指南](../website-docs/06-development/03-extension-points.md) | 沿用本版已合入的修订 | 保留厂商注册、联网搜索参数和扩展实现约定。 |
| [沙箱部署与排障](../website-docs/06-development/04-sandbox-deployment.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [对话提示词拼装与可编辑范围](../website-docs/06-development/05-agent-prompts.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [WeMM Transformers 部署](../website-docs/06-development/06-wemm-transformers.md) | 保留 | 本轮沿用正文，参加整站链接、锚点与构建检查。 |
| [v0.8.0 版本说明](../website-docs/07-releases/v0.8.0.md) | 保留历史 | 原发布内容与原有页面链接保留。 |
| [v0.8.2 版本说明](../website-docs/07-releases/v0.8.2.md) | 保留历史 | 原发布内容与原有页面链接保留。 |
| [v0.8.3 版本说明](../website-docs/07-releases/v0.8.3.md) | 保留历史 | 原发布内容与原有页面链接保留。 |
| [v0.9.0 版本说明](../website-docs/07-releases/v0.9.0.md) | 保留历史 | 原发布内容与原有页面链接保留。 |
| [WeKnora v0.9.1](../website-docs/07-releases/v0.9.1.md) | 新增 | 新增官网图文、升级步骤和回退说明。 |
