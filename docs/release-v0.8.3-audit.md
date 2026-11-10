# v0.8.3 发布整理与文档核对记录

## 基线与范围

- 上个版本：`v0.8.2`（`3e8b0bfc80b845b2d4b2ed683994748741450a97`）。
- 本次远程 main 截止：`31c6055687d33f9ad7a92cf453a264ce13a1700d`，2026-10-13 拉取，共 282 个提交。
- 初始整理分支：`chore/release-v0.8.3-docs`；发布 PR 分支：`chore/release-v0.8.3-website`。2026-10-14 提取到最新 `github_public/main`（`3b3b519310721295162ea94b9fd62a92c8cab341`）；相较盘点快照增加的 #4278 和 #4283 为基线已有修复，不属于本 PR 的新增代码。
- 版本选择：延续当前 0.8 系列使用下一个未占用版本 0.8.3；存在需要迁移的行为变化，已单列升级说明。发布日期确定为 2026-10-14；本 PR 更新发布文档与版本元数据，tag 和 GitHub Release 由发布流程创建。
- 审查对象是版本净变更对应的用户文档、配置/API 契约与发布内容；不是对 282 个提交重新进行完整安全审计。最终生效行为以该 main 快照的源码为准，提交标题只用于完整盘点。
- 插件系统的 11 个引入提交由 `6c83d7ab4` 撤回，没有作为新功能发布。DeepSeek Harness 等已有外部客户端集成不受此结论影响。

## 已修正与补齐

| 主题 | 原有偏差或遗漏 | 核对源码与处理 |
| --- | --- | --- |
| 会话附件 | 未区分解析与读取时机、所有附件 24 小时过期；Agent 说明把阅读绑定到沙箱 | `temporary_document.go`、`temporary_document_prompt.go`、`attachment/reader.go`、repository 的 `expiredQuery`；上传后后台解析、Agent 按需读取并等待、Quick Answer 开流后等待、有效消息引用保留，覆盖会话/Agent/API/配置文档 |
| 工具选择 | 空数组回退默认、智能推理至少选一项 | `tools/allowed.go`、`custom_agent_tools.go`、迁移 116/35；区分 null 与 []，补工具目录 API、未知名称与开关管理项处理 |
| 注册策略 | 快速上手、总览、配置与系统设置仍将 invite_only 称作邀请注册 | config/handler 与 `auth.registration_mode`；统一三档语义，单列升级动作 |
| 用户管理 | 没有用户列表/启停 API，创建用户入口仍在账户与访问 | `SystemSettings.vue`、`UserManagement.vue`、`system.go`、router；补列表分页、停用撤销会话、保护条件与新入口 |
| 画廊 | 只有底层图片处理参数，没有用户流程和完整 API | `knowledge.go`、`routes_knowledge.go`、`types/image_gallery.go`；补画廊流程、属性契约、GET/POST 过滤与不同 ID 上限 |
| 图片检索 | 开关要求在长篇底层说明中，缺少检索入口概述 | `knowledgebase_search_image.go`、图片索引与重排/回答链路；区分画廊、图片向量与文本描述，明确默认关闭和重解析 |
| 图片观察 | 将兜底误写为任何观察失误都不会丢内容 | `image_attr_decision.go`；说明 on_unobserved 可关闭兜底，完整但错误的负值观察也可能跳过 OCR |
| 模型 | 已加入 Cohere 但仍计数 29，扩展指南遗漏 TEI/Pinecone 协议 | providers 的 Builtins/测试明确 30；更新数字与协议列表，补 WeMM 默认会话批处理说明 |
| 数据库 | 总清单仍停在 PG 110 / SQLite 30，并声称 SQLite 无 MCP metadata | 迁移文件 111–118 / 31–36；补投影回填、索引、OAuth 时间戳，修正默认镜像和附件状态 |
| MCP 授权 | 文档声称授权完成关闭整个服务连接 | `CloseClientsAfterAuthorization`；当前主体与旧 client_id 连接清理，补读取注册失败的保守回退，避免夸大为永不影响其他用户 |
| 本机技能 | 只介绍本机命令执行，漏掉安装及激活版本 | `tenant_skill_host.go`、`host_skills.go`；补本机安装目标、版本切换与失败保留旧版 |
| Wiki/引用/截图 | 旧图不能展示新分组和原文定位 | 更新说明，接入真实产品截图并核对图注；保留历史版本图片 |
| 配置 | 遗漏知识库描述预算和出站拨号超时 | `.env.example`、config、描述生成服务；补 2048 token 与 5s 默认，区分拨号和总请求超时 |
| 发布入口 | 首页、版本导航、徽章仍指 v0.8.2 | v0.8.3 页面、首页画廊、四种 README、共享导航、检查脚本与当前版本元数据统一；历史说明与依赖独立版本保留 |

## 首页长期内容调整

- 中文功能名与前端一致使用「画廊」；画廊与多模态 Embedding 在首页、版本说明、知识库指南及四语 README 分开介绍。
- 多模态 Embedding 先说明对这一模型类型的支持，以腾讯 WeMM-Embedding、Qwen3-VL-Embedding 等作为已支持的示例；WeMM 的具体预设和部署方式在详情展开，保留启用条件与现有部署文档入口。
- 本机浏览器使用与版本号无关的独立图文区并复用真实截图；MCP Server 归入生态集成，对话追加、分叉与回滚归入 Agent 概述，不再等权展示。技能与沙箱、Wiki 保留专题，Lite 本机项目目录补入沙箱说明。
- 语言新增等较小更新不再占据首页版本补充栏，仍在版本说明保留。首页模型厂商数修正为 30。

## 发布表达与历史约定

参考 2026-09-23 Claude 的「v0.8.0 到 v0.8.2 文档同步检查」对话和 v0.8.2 版本页：

- 首页重点说明用途，简短文案配大图；配置前提与协议细节放入功能文档。
- 当期版本区保留四项重点：画廊、多模态 Embedding、Wiki 图谱、原文引用定位，采用左侧紧凑功能导航与右侧截图，四项说明均保持展开，优先让说明与图片同屏；附件按需读取、平台用户管理、Seafile 与 Docker 终端使用次级链接。
- 首页 01–07 及使用入口统一背景交替、全宽背景、内容容器和章节间距；浅色背景区统一提供分隔线，深色模式复用主题变量。
- 本机浏览器保留独立专题，恢复「执行任务／连接扩展」两张图、BrowserSkill 图标与 GitHub 入口，左侧分别展开「执行任务」「连接扩展」的说明，右侧切换对应截图，与技能沙箱之间补齐边界和背景区分。
- 版本页按用户可以做什么组织，保留升级操作与限制；提交数量、快照、撤回记录在本报告保留，完整技术更新仍见 CHANGELOG。

- Wiki 图谱渲染与交互调整单列版本章节；核对 #4147 的分组布局、主题说明、标签避让、分组切换和返回全图，首页 Wiki 专题首项使用分组图谱实拍截图。

## 第二轮整体复核（2026-10-14）

- 再次按 v0.8.2 至既定 main 快照的 282 个提交核对首页与版本页，未改变发布基线。
- 首页生态列表补 Seafile，复用前端现有图标；技能专题补 macOS Lite 本机安装。明确 Docker/E2B/Cube 均有终端，图形桌面只适用于 E2B/Cube 桌面模板。
- 版本页补共享智能体操作调用者本机浏览器、工具列表搜索、检索参数与诊断、渠道会话管理、模型完成事件的实际模型 ID。跨库 Wiki 搜索明确为 API 能力，放回 Wiki 章节。
- 补扫描 PDF 整页不生成图片向量、原文引用的访问权限与无原文件回退、Seafile 单个未加密资料库及不支持 SeaDoc 的边界。
- 修正 CHANGELOG 将 #3950 的实体知识图谱方向/来源修复归到 Wiki 的错误；两类图谱分别说明。
- Swagger 原产物遗漏 10 个现有路径；使用与 go.mod 一致的 swag v1.16.6 重新生成。补模型目录读取/预览/发布的 3 个接口注释（2 个路径），正确声明仅管理员 JWT、版本冲突与 JSON overlay 对象；修复 spans 注释重复 API 基路径及缺失鉴权声明。三种产物均由 `make docs` 生成，不手改产物。
- 本次新增后端改动仅为 Swagger 注释及 schema 标签，不改接口行为。契约检查覆盖新发布接口、基路径不得重复、overlay 对象类型。

## 发布内容与截图

- 面向用户的版本说明：`website-docs/07-releases/v0.8.3.md`。
- 英文分类更新清单：根目录 `CHANGELOG.md`；以下附录保留全部提交，可追溯未单独展开的测试/构建/修正文档提交。
- 截图清单：`website-docs/README.md` 的「v0.8.3 截图清单」，8 个本次场景 + 1 个既有未补齐场景；4 个首页场景复用对应文件。
- 已接入用户提供的 11 张真实截图并逐张检查，画廊、多模态设置、Wiki 分组图谱、引用定位均已同步到首页。画廊截图保留了原始界面中的部分图片加载失败状态；图注已按实际展示内容调整。未合成产品界面、未执行浏览器外观验收。

## 验证记录

- 使用 Node 24.19.0 执行 `npm run build --prefix website-docs`，成功完成 Next.js 首页与 VitePress 文档构建、内部链接检查、60 个 Mermaid 图检查（0 失败）、首页内容审计和统一站点检查（77 页、376 个本地路由/资源）。
- `npm run lint --prefix website-docs/homepage` 通过；`git diff --check` 通过。发布分支的 `docs`、`internal/handler`、`internal/application/service` 相对 `github_public/main` 的增量 Go lint 为 0 issues。
- `go test ./docs` 通过，覆盖 Go 注册文档、swagger.json 和 swagger.yaml 的路由/schema 契约；再次执行 `make docs` 后三份产物逐字节一致。
- 根 VERSION、文档 VERSION、前端/文档 package.json 与 lockfile 根版本、Helm appVersion 均核对为 0.8.3；`scripts/get_version.sh json` 输出 0.8.3。两个 lockfile 除根版本字段外与 main 完全一致，没有依赖变更。
- 校验附录包含全部 282 个提交，PostgreSQL 迁移文件为 238 个；历史版本页未修改。
- 首页 `prebuild` / `predev` 自动同步复用截图，更新文档源图后无需手动复制；同步回归覆盖替换源图、终端文件名映射及保留首页独有图片。预览中的 Wiki 图片已按 HTTP 响应逐字节核对为最新源图，顶部留白收紧，WeMM 名称链接至官方仓库。
- 静态站点产物位于 `website-docs/static-site/`。11 张文档截图及首页复用的 5 张截图均已补齐；未执行浏览器外观验收或 Docker 部署测试。
- 本 PR 仅包含文档、官网内容、截图、版本元数据与 Swagger 注释/生成物。附件读取修复已单独通过 #4283 合入，本 PR 不重复携带其代码或测试。
- 未运行全量 Go/前端业务回归或实际数据库升级。迁移说明依据当前源文件核对，不能替代生产备份与升级演练。
- 依赖安装提示已有依赖漏洞，未在本次发布文档整理中升级依赖；构建存在依赖包 chunk-size 提示，不影响构建结果。

## 全部提交（时间顺序）

「已撤回」不进入有效功能列表；「构建/测试/文档」仍保留在盘点中。其余按主题归入 CHANGELOG 的新增、改进、修复和升级事项。

| 日期 | 提交 | 内容 | 处理 |
| --- | --- | --- | --- |
| 2026-09-24 | [cee697920](https://github.com/Tencent/WeKnora/commit/cee697920e0aebf067b57cf9612aa330863b17c3) | fix(website-docs): serve everything but the homepage from /docs/ | 有效变更，按主题归纳 |
| 2026-09-24 | [b6b32c24e](https://github.com/Tencent/WeKnora/commit/b6b32c24e152650cf94083c21195fe8bd24328f5) | test(website-docs): update integration test for the v0.8.2 header and release gallery | 构建/测试/文档或体验调整 |
| 2026-09-24 | [8d4015014](https://github.com/Tencent/WeKnora/commit/8d40150149ce00a952a98a10a80226ad6584abd5) | fix(session): keep auto-generated titles plain text | 有效变更，按主题归纳 |
| 2026-09-24 | [967ed0970](https://github.com/Tencent/WeKnora/commit/967ed097062c61723045ae103b665982728dc572) | feat(models): add system admin model catalog console (#3662) | 有效变更，按主题归纳 |
| 2026-09-24 | [eebc1d864](https://github.com/Tencent/WeKnora/commit/eebc1d86436e9cd7b9f6d24c1f973527f318853f) | fix(ui): restore inner padding on form popups (#3673) | 有效变更，按主题归纳 |
| 2026-09-24 | [e6284f4ea](https://github.com/Tencent/WeKnora/commit/e6284f4eacc89a4b2f1ef2469e9f86241e2841cd) | fix(browser): let shared-space agents drive the caller's local browser | 有效变更，按主题归纳 |
| 2026-09-24 | [5d00985f7](https://github.com/Tencent/WeKnora/commit/5d00985f720440f88c88ccdf8f65232175d885d5) | docs: restructure the English and Chinese READMEs | 构建/测试/文档或体验调整 |
| 2026-09-24 | [44a1df604](https://github.com/Tencent/WeKnora/commit/44a1df60460fc06fba9eed9022d57164605a487c) | docs(readme): website-matched design for all four READMEs (#3679) | 构建/测试/文档或体验调整 |
| 2026-09-24 | [65d8129cb](https://github.com/Tencent/WeKnora/commit/65d8129cb64f4ccc23a7b73896ebe424d31ba228) | fix(retriever/es): keep vectors and enabled state on CopyIndices (#3678) | 有效变更，按主题归纳 |
| 2026-09-24 | [3cf0be2e9](https://github.com/Tencent/WeKnora/commit/3cf0be2e9745b97dd903feec1c66a936780237d8) | fix(embed): warn at startup when no signing key is configured | 有效变更，按主题归纳 |
| 2026-09-24 | [e22a8367e](https://github.com/Tencent/WeKnora/commit/e22a8367efc9802353e6981e162226459a998404) | feat(docparser): support MinerU 4.0 V1 API for self-hosted MinerU | 有效变更，按主题归纳 |
| 2026-09-24 | [9253b2c1d](https://github.com/Tencent/WeKnora/commit/9253b2c1dc8f4fe83fec8e66ba79dbf00144fd43) | fix(frontend): link docs to the public docs site instead of GitHub | 有效变更，按主题归纳 |
| 2026-09-24 | [9fc08c207](https://github.com/Tencent/WeKnora/commit/9fc08c2079d89173c17c61356dee3838772931c5) | feat(retrieval): unify rerank across search APIs and expose diagnostics (#3675) | 有效变更，按主题归纳 |
| 2026-09-24 | [65d8a34ea](https://github.com/Tencent/WeKnora/commit/65d8a34ead4c99991d450ecc8c0acdbcf5b0e583) | fix(desktop): pair the Lite browser against the local API listener (#3677) | 有效变更，按主题归纳 |
| 2026-09-24 | [c029b50ef](https://github.com/Tencent/WeKnora/commit/c029b50ef928d5c21ffb1922f6d541b0d27bb7a4) | fix(rss): 拒绝超限响应以避免正文静默截断 | 有效变更，按主题归纳 |
| 2026-09-24 | [7e82ee33d](https://github.com/Tencent/WeKnora/commit/7e82ee33dcefc398ca455775700bdf8730054aa5) | docs(misc): add intro video source | 构建/测试/文档或体验调整 |
| 2026-09-24 | [e2433b129](https://github.com/Tencent/WeKnora/commit/e2433b1293fa350d972902cd3676a1aa9a9c9aac) | feat(lite): install and run skills on the host (#3661) | 有效变更，按主题归纳 |
| 2026-09-25 | [e896270c4](https://github.com/Tencent/WeKnora/commit/e896270c418c01d230c9c6d11f05d0148677892a) | docs(readme): surface agent tools in the overview and condense feature tables (#3688) | 构建/测试/文档或体验调整 |
| 2026-09-25 | [04adc1327](https://github.com/Tencent/WeKnora/commit/04adc132726d93c9247898af79ad8f3b542df4f0) | fix(retrieval): fix recall correctness, score scales, cost and agent tools (#3687) | 有效变更，按主题归纳 |
| 2026-09-25 | [68fe9599c](https://github.com/Tencent/WeKnora/commit/68fe9599cdb853eb1205499d44ed176613f875df) | feat(gallery): add an image gallery tab with schema-driven filters (#3694) | 有效变更，按主题归纳 |
| 2026-09-25 | [ebbcf96fa](https://github.com/Tencent/WeKnora/commit/ebbcf96fabb651c091c3ee2272d802b80c100c4e) | feat(gallery): rework the gallery toolbar, header tabs and image viewer | 有效变更，按主题归纳 |
| 2026-09-25 | [1bf717a54](https://github.com/Tencent/WeKnora/commit/1bf717a54d4413e0e5cfe5e2b7ff008489b97c04) | feat(chat): open cited documents at the cited passage (#3700) | 有效变更，按主题归纳 |
| 2026-09-25 | [ab115c094](https://github.com/Tencent/WeKnora/commit/ab115c0944b6e7f3c5d976970b67cb6070f6db1b) | fix(knowledge): stop parses from getting stuck in processing or finalizing (#3696) | 有效变更，按主题归纳 |
| 2026-09-25 | [8619bdf3c](https://github.com/Tencent/WeKnora/commit/8619bdf3c5149acc8ca51263e4d485f97da39211) | docs(readme): swap in the new intro video | 构建/测试/文档或体验调整 |
| 2026-09-25 | [ef6f28c1c](https://github.com/Tencent/WeKnora/commit/ef6f28c1cafec72bd05fcb4fbd3feb8144f93e3d) | fix(agent): recover tool panics instead of crashing the server | 有效变更，按主题归纳 |
| 2026-09-25 | [8356742dd](https://github.com/Tencent/WeKnora/commit/8356742dd5a75cc84a5f18a527665eac4abf5bea) | fix(llm): treat cut-off streams as broken and tighten LLM retries | 有效变更，按主题归纳 |
| 2026-09-25 | [ef938f911](https://github.com/Tencent/WeKnora/commit/ef938f9114cc82e799c56b5bde2b3aca9bc3832c) | fix(logger): keep LLM debug records inside their directory | 有效变更，按主题归纳 |
| 2026-09-25 | [8a75bece6](https://github.com/Tencent/WeKnora/commit/8a75bece6d491a21e8b7b4c6171a8151ad77da9b) | fix(mcp): close MCP connections on shutdown without blocking exit | 有效变更，按主题归纳 |
| 2026-09-25 | [27b0c84bf](https://github.com/Tencent/WeKnora/commit/27b0c84bfdb2641c66942aea4620f44b328096e1) | fix(docparser): make the MinerU parse timeouts configurable | 有效变更，按主题归纳 |
| 2026-09-25 | [929dad574](https://github.com/Tencent/WeKnora/commit/929dad5747fb24e3cc85ec86eb1f4b4a3a497441) | fix(weaviate): accept a class another worker created first (#3689) | 有效变更，按主题归纳 |
| 2026-09-25 | [605d70fc5](https://github.com/Tencent/WeKnora/commit/605d70fc528a948591b647b0b805e7bfd617d4be) | fix(notion): 在资源发现失败时中止增量同步 (#3682) | 有效变更，按主题归纳 |
| 2026-09-25 | [5614c04d8](https://github.com/Tencent/WeKnora/commit/5614c04d88f2c28620a6bd3e16fc1093b9c54539) | fix(feishu): stop Wiki pagination on repeated page tokens (#3645) | 有效变更，按主题归纳 |
| 2026-09-25 | [6eb3cce16](https://github.com/Tencent/WeKnora/commit/6eb3cce164f6b1efe9c3a02973502c93f7d45126) | fix(agent): 合并相邻 assistant 消息时保留 tool_calls，避免孤儿 tool 消息导致整轮 400 (#3574) | 有效变更，按主题归纳 |
| 2026-09-25 | [e5a301ed3](https://github.com/Tencent/WeKnora/commit/e5a301ed3ad156d7139231b43f7664e05eea6806) | fix(files): accept canonical storage:// paths in bare provider services (#3166) | 有效变更，按主题归纳 |
| 2026-09-25 | [5c717ca04](https://github.com/Tencent/WeKnora/commit/5c717ca045ab6742b5bb57dc1669a54330f6b65b) | fix(im): handle unresolved Feishu resource images (#2761) | 有效变更，按主题归纳 |
| 2026-09-25 | [591f1c0e4](https://github.com/Tencent/WeKnora/commit/591f1c0e4fb70a9ece1d807b19c6fa0c24e6b102) | docs: drop references to source files that no longer exist (#3631) | 构建/测试/文档或体验调整 |
| 2026-09-25 | [d9444683d](https://github.com/Tencent/WeKnora/commit/d9444683dae2f382e29655f3efc6cc156d199329) | docs(dev): 补充 Swagger 容器访问与空白页排查步骤 (#3620) | 构建/测试/文档或体验调整 |
| 2026-09-25 | [0cb1e57fa](https://github.com/Tencent/WeKnora/commit/0cb1e57fae586908ca8d005459eb82acf54ca015) | fix(container): propagate host project lookup errors (#3611) | 有效变更，按主题归纳 |
| 2026-09-25 | [4668447aa](https://github.com/Tencent/WeKnora/commit/4668447aa60b5190f90a8a53546151df4ec4ed11) | fix(helm): pin application images to chart version (#3669) | 有效变更，按主题归纳 |
| 2026-09-25 | [22404e92b](https://github.com/Tencent/WeKnora/commit/22404e92b79dfc3ae7d60342ba16b0fd37364b30) | style(files): wrap long test case to satisfy lll | 构建/测试/文档或体验调整 |
| 2026-09-25 | [508048aee](https://github.com/Tencent/WeKnora/commit/508048aee9739e4d034ab530d6a25603a6d9042d) | fix(frontend): mount the settings modal only once on /platform/settings | 有效变更，按主题归纳 |
| 2026-09-25 | [745a22d06](https://github.com/Tencent/WeKnora/commit/745a22d066cab0dc555372a7510c017c5ddb9c6a) | refactor(frontend): drop TTL resource caches and remove duplicate startup requests (#3562) | 构建/测试/文档或体验调整 |
| 2026-09-25 | [c7601b2d2](https://github.com/Tencent/WeKnora/commit/c7601b2d2aaf298939b5ce775970c3005044d74a) | feat(wiki): 增加跨知识库 Wiki 搜索 POST /wiki-search (#3535) | 有效变更，按主题归纳 |
| 2026-09-25 | [a9d71038b](https://github.com/Tencent/WeKnora/commit/a9d71038b6657fda5e01a85ad085b861c2021758) | fix(models): stop advertising graded thinking levels the wire format drops (#3552) | 有效变更，按主题归纳 |
| 2026-09-25 | [00e6366fd](https://github.com/Tencent/WeKnora/commit/00e6366fd27a42bca7b08d6fef5b909339bf6be9) | fix(datasource): validate cron schedules and persist manual-only mode (#3653) | 有效变更，按主题归纳 |
| 2026-09-25 | [0bf105499](https://github.com/Tencent/WeKnora/commit/0bf105499b093578ddbaabf24403ba0aa776657a) | fix(knowledge): reject invalid JSON uploads before async parse (#3360) | 有效变更，按主题归纳 |
| 2026-09-25 | [a7fc9e13d](https://github.com/Tencent/WeKnora/commit/a7fc9e13dab91cf5bfc3d33c0f09415879957868) | fix(client): accumulate multiline SSE data (#2122) | 有效变更，按主题归纳 |
| 2026-09-25 | [e7c27ccc9](https://github.com/Tencent/WeKnora/commit/e7c27ccc9e7fc806c583f6bb5de012cba12e5c53) | feat(confluence): add directory sync and private image assets (#3319) | 有效变更，按主题归纳 |
| 2026-09-25 | [a7ac21ce7](https://github.com/Tencent/WeKnora/commit/a7ac21ce769fa75854c0b7c7cfb04d934464fa73) | fix(docreader): sanitize non-conforming styles.xml fills before openpyxl loads (#3637) (#3638) | 有效变更，按主题归纳 |
| 2026-09-25 | [1dd3fe867](https://github.com/Tencent/WeKnora/commit/1dd3fe867e0d343df765961eb090ae0c72a3c031) | fix(docreader): drop data validation ranges openpyxl cannot parse instead of failing the sheet (#3599) (#3608) | 有效变更，按主题归纳 |
| 2026-09-25 | [4364e61af](https://github.com/Tencent/WeKnora/commit/4364e61afa4bf1e086c34b4220bb21449aa15b25) | feat(datasource/yuque): preserve TOC folder hierarchy for synced documents (#3476) | 有效变更，按主题归纳 |
| 2026-09-26 | [47773b5bd](https://github.com/Tencent/WeKnora/commit/47773b5bd1fc33ecbe96d11824d4aa4b97ae2438) | fix(frontend): initialize chat citation popover after botmsg props | 有效变更，按主题归纳 |
| 2026-09-26 | [c91a8d11f](https://github.com/Tencent/WeKnora/commit/c91a8d11f8eaa1370970355019a001a36e993117) | feat(plugin): plugin system foundation — builtin plugins, config schemas and workspace switches (#3711) | 已撤回 |
| 2026-09-26 | [ed63e35cc](https://github.com/Tencent/WeKnora/commit/ed63e35cc7d49856c562b9f519534cba6d6b9ee8) | feat(plugin): install declarative plugins — MCP servers, skills and model vendors (#3716) | 已撤回 |
| 2026-09-26 | [4328ab26a](https://github.com/Tencent/WeKnora/commit/4328ab26a942a3fb9c397dbea57b41ac1074cc46) | feat(plugin): code plugins — extension protocol, Go SDK, plugin host, web search and data source adapters (#3720) | 已撤回 |
| 2026-09-26 | [ff49f9491](https://github.com/Tencent/WeKnora/commit/ff49f9491d6b73de5adea18f061a7d04073cbd44) | feat(plugin): Host API with a key-value store, and document parsers from plugins (#3721) | 已撤回 |
| 2026-09-26 | [728007ada](https://github.com/Tencent/WeKnora/commit/728007ada9df5461d1f70aca64ec358718b891bd) | feat(plugin): remote runtime, Python SDK and python host plugins (P2 batch 3) (#3724) | 已撤回 |
| 2026-09-26 | [da4ef68d9](https://github.com/Tencent/WeKnora/commit/da4ef68d9d5327dfd236299d882f092943a1a165) | feat(plugin): standalone plugin hosts with Redis discovery (P2 batch 4) (#3725) | 已撤回 |
| 2026-09-26 | [996696057](https://github.com/Tencent/WeKnora/commit/996696057f7ec229fd1930188955283042f0c852) | feat(plugin): sandboxed plugin pages and the @weknora/plugin-ui bridge (P2 batch 5) (#3727) | 已撤回 |
| 2026-09-26 | [cf502ba46](https://github.com/Tencent/WeKnora/commit/cf502ba46a629598db393d747be05da3df682c18) | feat(plugin): event delivery and webhooks for plugins (P2 batch 6) (#3728) | 已撤回 |
| 2026-09-26 | [01222aac8](https://github.com/Tencent/WeKnora/commit/01222aac8e884f9a14db531072c6074f059beb4a) | feat(plugin): dynamic form options, OAuth connections and resource limits (P2 batch 7) (#3729) | 已撤回 |
| 2026-09-26 | [4f5d475c3](https://github.com/Tencent/WeKnora/commit/4f5d475c3eebe47d7c0e921799bfdb8337f6a024) | feat(examples): Jira Cloud plugin with OAuth, dynamic options and a skill (#3730) | 已撤回 |
| 2026-09-26 | [a829d7cc9](https://github.com/Tencent/WeKnora/commit/a829d7cc9cc3ac96c3cd1bbd6210203966c97d56) | feat(plugin): agent tools served by plugins, with result views (P2 batch 8) (#3733) | 已撤回 |
| 2026-09-26 | [6c83d7ab4](https://github.com/Tencent/WeKnora/commit/6c83d7ab479212c136094182a68ac6e238ea370c) | revert: take the plugin system back out of main | 撤回插件系列 |
| 2026-09-26 | [9fcc2c674](https://github.com/Tencent/WeKnora/commit/9fcc2c674309b42a1d4077e6b4c2a2334d49e57c) | fix(dev): propagate dev.sh exit status and pull MinIO from pgsty/minio | 有效变更，按主题归纳 |
| 2026-09-26 | [4df7ccbb4](https://github.com/Tencent/WeKnora/commit/4df7ccbb4f650082331452a92b52854f8922da2a) | fix(chat): drop stale minimap-target class binding | 有效变更，按主题归纳 |
| 2026-09-27 | [9114e4e4f](https://github.com/Tencent/WeKnora/commit/9114e4e4f905be71976a77c684d3f92731e6f9a6) | fix(citations): 修复原文定位、高亮范围与 DocReader 启动失败 (#3794) | 有效变更，按主题归纳 |
| 2026-09-28 | [a570dffcf](https://github.com/Tencent/WeKnora/commit/a570dffcfa51ad92a5c66a8260ceaaa831567dc0) | test(wiki): 表格夹具改用合成数据 | 构建/测试/文档或体验调整 |
| 2026-09-28 | [5bf2ae1cc](https://github.com/Tencent/WeKnora/commit/5bf2ae1cc27aa555957dc1c258968c5b36246b26) | fix(faq): actually clean FAQ entry payloads before validation and storage | 有效变更，按主题归纳 |
| 2026-09-28 | [0b463e459](https://github.com/Tencent/WeKnora/commit/0b463e459aa70ea42036a49664a75e4da95684bf) | test(skills): wait for the host install to release its lock before asserting | 构建/测试/文档或体验调整 |
| 2026-09-28 | [9cab22685](https://github.com/Tencent/WeKnora/commit/9cab2268531477f6cd879acfd975189973d099cd) | docs: extract active tenant ID in the API quickstart | 构建/测试/文档或体验调整 |
| 2026-09-28 | [157fb4d3c](https://github.com/Tencent/WeKnora/commit/157fb4d3c1763c47fdda88aa4133b37439bbc54a) | fix(frontend): label host-sandbox skill secret cards like the sandbox cards | 有效变更，按主题归纳 |
| 2026-09-28 | [bdad5eea2](https://github.com/Tencent/WeKnora/commit/bdad5eea2b46546db4c1481fc83d62bd69351fcc) | fix(chat): clear stale KB scope on session restore | 有效变更，按主题归纳 |
| 2026-09-28 | [d528d1fc3](https://github.com/Tencent/WeKnora/commit/d528d1fc3ca60f331befe1ba8c0ce28962024bd3) | fix: skip table-summary tasks for missing documents | 有效变更，按主题归纳 |
| 2026-09-28 | [d270a844a](https://github.com/Tencent/WeKnora/commit/d270a844ac7684ed6eeea7e0469f3f5e37aef8ca) | fix: reject empty table-summary model responses | 有效变更，按主题归纳 |
| 2026-09-28 | [675b06add](https://github.com/Tencent/WeKnora/commit/675b06addcd76a9f9f0c515bcd6554008efc67d3) | test: consolidate table summary regressions | 构建/测试/文档或体验调整 |
| 2026-09-28 | [bad7afa6a](https://github.com/Tencent/WeKnora/commit/bad7afa6ae3198b8e574c94614e41267dbe6e005) | fix(sandbox): pass desktop slot timing to the renew goroutine | 有效变更，按主题归纳 |
| 2026-09-28 | [4ba85e2ed](https://github.com/Tencent/WeKnora/commit/4ba85e2edf71a041fa6846c14eb74ccfa0cfb389) | fix(wiki): fan out follow-up triggers so backlogs drain at ingest_max_inflight | 有效变更，按主题归纳 |
| 2026-09-28 | [c819abd95](https://github.com/Tencent/WeKnora/commit/c819abd9589c23a3fa504f946fc477499cd0fe70) | fix(wiki): bound outstanding follow-up tasks | 有效变更，按主题归纳 |
| 2026-09-28 | [ec0ca4633](https://github.com/Tencent/WeKnora/commit/ec0ca46334556e5b49a981e9cf26189e162eca31) | fix(server): run ResourceCleaner on signal shutdown instead of racing Fatalf exit (#3804) | 有效变更，按主题归纳 |
| 2026-09-29 | [08f7e702c](https://github.com/Tencent/WeKnora/commit/08f7e702cfb3a1344b537641739fa08c523df255) | fix(datasource/dingtalk): 跳过不再静默，展开叶子不再报错 | 有效变更，按主题归纳 |
| 2026-09-29 | [7d1d71e50](https://github.com/Tencent/WeKnora/commit/7d1d71e50d7123dc14c749d5677b588f728dc7c9) | fix(datasource/dingtalk): 块级渲染不再静默丢内容（列表按线上真实形状渲染 + 未建模块类型可见） (#3822) | 有效变更，按主题归纳 |
| 2026-09-29 | [72e105f46](https://github.com/Tencent/WeKnora/commit/72e105f46a7747d5acb1a887bd459419d4b9b24d) | fix(gitlab): 按提交快照比较增量同步变更 | 有效变更，按主题归纳 |
| 2026-09-29 | [a282d1091](https://github.com/Tencent/WeKnora/commit/a282d1091ebf8c5d7d8d64796a25f1e1527bd296) | fix(feishu): 云盘分页补上 #3645 给 wiki 加的重复 page token 防护 (#3770) | 有效变更，按主题归纳 |
| 2026-09-29 | [b9be7d0d0](https://github.com/Tencent/WeKnora/commit/b9be7d0d0d525435b67caeb49ef0502d6b6e7097) | fix(notion): 修复页面清空后旧正文残留 | 有效变更，按主题归纳 |
| 2026-09-29 | [15d484552](https://github.com/Tencent/WeKnora/commit/15d484552140eafe491e01d7f7bbfb558515801b) | fix(rss): 修复全文回退后增量同步不重试 | 有效变更，按主题归纳 |
| 2026-09-29 | [cf079b8dc](https://github.com/Tencent/WeKnora/commit/cf079b8dcd3315b46f1a2c848837f851ec61e04c) | fix(models): keep Retry-After on HTTPError and honour it in agent LLM retries | 有效变更，按主题归纳 |
| 2026-09-29 | [436d02fdf](https://github.com/Tencent/WeKnora/commit/436d02fdf3865279df3ff3bc14a91057b56d0464) | fix(compaction): 流因取消而关闭时不得当成空摘要成功返回 | 有效变更，按主题归纳 |
| 2026-09-29 | [e39043f34](https://github.com/Tencent/WeKnora/commit/e39043f34b22bec26ed6ddb4d08ff66e9555f3ea) | fix(repository): 关键词 LIKE/ILIKE 检索补显式 ESCAPE，修复 Lite 上静默漏结果 | 有效变更，按主题归纳 |
| 2026-09-29 | [1912ba26c](https://github.com/Tencent/WeKnora/commit/1912ba26c544025adb869459a74c2cbac2a95cd9) | test(weaviate): 让并发建表的用例真的每个 worker 都去创建，消掉随机失败 | 构建/测试/文档或体验调整 |
| 2026-09-29 | [f46c99056](https://github.com/Tencent/WeKnora/commit/f46c9905677b36a454aa6669a2619f9eecf9fd23) | fix(embed): 开启 Host context 后点击追问建议不再返回 400 | 有效变更，按主题归纳 |
| 2026-09-29 | [75d2c1b58](https://github.com/Tencent/WeKnora/commit/75d2c1b58343b2872051a24b78185298fb8b88f3) | fix(frontend): normalize style guard paths | 有效变更，按主题归纳 |
| 2026-09-29 | [0c6b30cfa](https://github.com/Tencent/WeKnora/commit/0c6b30cfa705c1d2f2ccd084cd76174738a7fb4b) | fix(docs): spawn nested npm through Node on Windows | 有效变更，按主题归纳 |
| 2026-09-29 | [9842e51d4](https://github.com/Tencent/WeKnora/commit/9842e51d40a54ca2dbb61153f3fb587da1265b7a) | fix(frontend): 修复复制模型时模型名称被改写的问题 (#3830) | 有效变更，按主题归纳 |
| 2026-09-30 | [5225edc0f](https://github.com/Tencent/WeKnora/commit/5225edc0f746ae68e15dc4bd6fde9343efde1142) | feat(auth): 新增仅限邀请注册档位 (#3900) | 有效变更，按主题归纳 |
| 2026-09-30 | [176aa0d02](https://github.com/Tencent/WeKnora/commit/176aa0d02b30b64634d55d000b6b44e00ec0310a) | fix(sandbox): build real arm64 sandbox images and ship arm64 Cube variants | 有效变更，按主题归纳 |
| 2026-09-30 | [dba912755](https://github.com/Tencent/WeKnora/commit/dba9127558c3df160c8ff181e2a0a99172ae7c75) | docs(api): 补充模型复制接口说明 | 构建/测试/文档或体验调整 |
| 2026-09-30 | [2ec177462](https://github.com/Tencent/WeKnora/commit/2ec1774623a77608902287b2ba42dcde3e4daaab) | fix(auth): 令牌写入失败不再当成登录成功，查询失败不再谎称已撤销 | 有效变更，按主题归纳 |
| 2026-09-30 | [b71b31c89](https://github.com/Tencent/WeKnora/commit/b71b31c897efae7b6b8320ab0e5bdf796eb1e006) | fix(ima): 笔记正文被清空后旧正文仍留在知识库 | 有效变更，按主题归纳 |
| 2026-09-30 | [1dbe9d0c1](https://github.com/Tencent/WeKnora/commit/1dbe9d0c1a1b9dd8d7c345c5c1672f76269b86c5) | fix(gitlab): 仓库文件下载补体积上限，超限报错而不是无界读入 (#3887) | 有效变更，按主题归纳 |
| 2026-09-30 | [75fc0ad15](https://github.com/Tencent/WeKnora/commit/75fc0ad1590786202af57546a6879bb074671ab9) | feat(docparser): upgrade AnyDoc to 0.2.4 and parse assets once | 有效变更，按主题归纳 |
| 2026-09-30 | [bccb4b151](https://github.com/Tencent/WeKnora/commit/bccb4b151bae403508da77fbb174efc79dc47c1a) | fix(embed): 内嵌网页只传图片不输入文字时不再返回 400 | 有效变更，按主题归纳 |
| 2026-10-07 | [402f69832](https://github.com/Tencent/WeKnora/commit/402f69832e395d8d193d89fef7a248a1c48ad24c) | fix(notion): 查询撞厂商上限被截断时不再静默，截断轮次不下发删除 (#3845) | 有效变更，按主题归纳 |
| 2026-10-07 | [0ec945fe5](https://github.com/Tencent/WeKnora/commit/0ec945fe5df0def761351aa640bdb588bd8e1bb2) | fix(dataset): use supported default QA model (#3965) | 有效变更，按主题归纳 |
| 2026-10-07 | [72b8a106c](https://github.com/Tencent/WeKnora/commit/72b8a106c77a3337bd5d785c595c8575686b6025) | fix(sqlite): 检索后端批量更新索引副本失败不再吞掉错误 (#3768) | 有效变更，按主题归纳 |
| 2026-10-07 | [a8a2c17b8](https://github.com/Tencent/WeKnora/commit/a8a2c17b892c378686e4d0f3916b064d2b38da80) | fix(sqlite): 索引行写入失败不再被丢弃，BatchSave 回滚而不是留下「主行在、索引行不在」 (#3843) | 有效变更，按主题归纳 |
| 2026-10-07 | [f9eeb8dc8](https://github.com/Tencent/WeKnora/commit/f9eeb8dc8535d678e206b00c6e24540db542b868) | fix(retriever/es): _bulk 的 per-item 失败不再被吞，全部被拒时 BatchSave 返回错误 (#3842) | 有效变更，按主题归纳 |
| 2026-10-07 | [5f5093b9f](https://github.com/Tencent/WeKnora/commit/5f5093b9f74e2c8621c11e1c53886758784d68b2) | fix(retriever): Qdrant/Milvus 关键词检索整批失败返回 error，不再当成「没有匹配」 (#3847) | 有效变更，按主题归纳 |
| 2026-10-07 | [5648d24a9](https://github.com/Tencent/WeKnora/commit/5648d24a93d353dca6d719178752e26d72a2478a) | fix(retriever/milvus): CopyIndices 按映射分块读源行，去掉会静默漏向量的 offset 窗口 (#3883) | 有效变更，按主题归纳 |
| 2026-10-07 | [9490035f1](https://github.com/Tencent/WeKnora/commit/9490035f1517bd68545df024e05b2244e4d1ac9a) | fix(agent): 图谱查询的三处截断必须可见，模型不再把子图当全集 (#3885) | 有效变更，按主题归纳 |
| 2026-10-07 | [9fa9e7c29](https://github.com/Tencent/WeKnora/commit/9fa9e7c29d8eb727825b14f0b0ca23907969b91a) | fix: use consistent qdrant env settings (#3960) | 有效变更，按主题归纳 |
| 2026-10-08 | [afc6cbd50](https://github.com/Tencent/WeKnora/commit/afc6cbd50264a54018faa9552346d45f964834fe) | fix(milvus): validate connections and preserve gRPC resolver defaults (#3654) | 有效变更，按主题归纳 |
| 2026-10-08 | [e29301115](https://github.com/Tencent/WeKnora/commit/e29301115a276afc4e7be7f0ffe568066c0c3386) | fix(graph): preserve relationship direction and document provenance (#3950) | 有效变更，按主题归纳 |
| 2026-10-08 | [7c284e7fc](https://github.com/Tencent/WeKnora/commit/7c284e7fc9beb065c4da2587b82bed52b5073bef) | fix(faq): 保留克隆状态同步时的正文和索引状态 (#3993) | 有效变更，按主题归纳 |
| 2026-10-08 | [a8c291c6f](https://github.com/Tencent/WeKnora/commit/a8c291c6ff52985e6729a8b3e0d404add08ecfd3) | fix: keep failed data-analysis table loads retryable (#3745) | 有效变更，按主题归纳 |
| 2026-10-08 | [27b9ee74a](https://github.com/Tencent/WeKnora/commit/27b9ee74a02f2f33689d0adb2de091c5bd8d5342) | fix(storage): use virtual-hosted addressing for Tencent COS through the S3 driver (#3722) | 有效变更，按主题归纳 |
| 2026-10-08 | [a15dfdd94](https://github.com/Tencent/WeKnora/commit/a15dfdd941497b32198f8abaec71510b304ee070) | fix(summary): 坏 JSON 不再冒充文档摘要（截断/畸形 JSON 走失败路径，纯文本兜底保留） (#3769) | 有效变更，按主题归纳 |
| 2026-10-08 | [e915adb14](https://github.com/Tencent/WeKnora/commit/e915adb147d721fc265b22db24a56a535b5bcf6c) | fix(chunker): preserve source offsets for table child chunks (#3969) | 有效变更，按主题归纳 |
| 2026-10-08 | [98feef38d](https://github.com/Tencent/WeKnora/commit/98feef38de5c39e57ef1c817e43ef6a4879f4029) | fix(chunker): count an escaped pipe as cell text when matching table widths (#3979) | 有效变更，按主题归纳 |
| 2026-10-08 | [0d3a053eb](https://github.com/Tencent/WeKnora/commit/0d3a053eb62c5150c8ad01442776de0225d06ea2) | fix(yuque): don't acknowledge failed detail fetches in the incremental cursor (#3702) | 有效变更，按主题归纳 |
| 2026-10-08 | [b5c74b2dd](https://github.com/Tencent/WeKnora/commit/b5c74b2dd6f183f1e31ef98a166696664b716570) | fix(feishu): 多维表格字段分页补重复 page_token 防护与条数上限 (#3840) | 有效变更，按主题归纳 |
| 2026-10-08 | [a2aeb97a0](https://github.com/Tencent/WeKnora/commit/a2aeb97a01203b23b6093eba2369583b460aaa66) | fix(gitlab): 分页循环补重复页防护与跳数上限 (#3841) | 有效变更，按主题归纳 |
| 2026-10-08 | [a6c6c35ad](https://github.com/Tencent/WeKnora/commit/a6c6c35ad292716570c44a42221f9e5ec68d2543) | fix(ima): 三条游标分页循环补重复游标防护与跳数上限 (#3889) | 有效变更，按主题归纳 |
| 2026-10-08 | [c9723b119](https://github.com/Tencent/WeKnora/commit/c9723b1198bfa42d31278b4e44f1199593ee5a08) | fix(docreader): read EPUB chapters in spine order (#3977) | 有效变更，按主题归纳 |
| 2026-10-08 | [9de0946e5](https://github.com/Tencent/WeKnora/commit/9de0946e53348d063180b2af87008a51c4fd9dcc) | fix(docparser): anydoc 内嵌 EMF/WMF 图片的扩展名与 Rust 侧对齐 (#3938) | 有效变更，按主题归纳 |
| 2026-10-08 | [188d9026d](https://github.com/Tencent/WeKnora/commit/188d9026d8d344464676c7919c09730a5181adb6) | fix(models): honor OpenRouter native thinking for legacy defaults (#3807) | 有效变更，按主题归纳 |
| 2026-10-08 | [f6119ae5f](https://github.com/Tencent/WeKnora/commit/f6119ae5fd4032e00a621a2cc280972390d0cfa4) | fix(agent): 保留数据分析 SQL 字符串字面量 (#3812) | 有效变更，按主题归纳 |
| 2026-10-08 | [52893006a](https://github.com/Tencent/WeKnora/commit/52893006ad26558164ddceabce89360a1b17b984) | fix(agent): validate tool string lengths by Unicode code points (#3937) | 有效变更，按主题归纳 |
| 2026-10-08 | [fa4f85d3d](https://github.com/Tencent/WeKnora/commit/fa4f85d3dd1edba7689a3e235abd3f159b2af381) | fix(agent): 拒绝将断流摘要保存为记忆检查点 (#3994) | 有效变更，按主题归纳 |
| 2026-10-08 | [e394c7ade](https://github.com/Tencent/WeKnora/commit/e394c7adecbe38019f66b8490ee06d254efaa05a) | fix(agent): 工具自起 goroutine 的 panic 自行兜底，MCP 预热不再带走进程 (#3846) | 有效变更，按主题归纳 |
| 2026-10-08 | [412ce63f0](https://github.com/Tencent/WeKnora/commit/412ce63f0b00ab45811f81b0ca254259e205acca) | fix(chat): clear stale file and tool selections on session restore (#3802) | 有效变更，按主题归纳 |
| 2026-10-08 | [f55947349](https://github.com/Tencent/WeKnora/commit/f559473498719ff0f5c94fc9c6918199986aaf3e) | fix(mcp): keep the client secret from dynamic client registration (#3763) | 有效变更，按主题归纳 |
| 2026-10-08 | [28734725a](https://github.com/Tencent/WeKnora/commit/28734725a3567d5d12a0b190b0e5bccbb91b252c) | fix(mcp): reconnect after the server forgets the session (#3765) | 有效变更，按主题归纳 |
| 2026-10-08 | [172d0a85a](https://github.com/Tencent/WeKnora/commit/172d0a85ab17e74abe8f987927ad1b9fd3eeb29a) | fix(mcp): pass embedded resources in tool results to the agent (#3766) | 有效变更，按主题归纳 |
| 2026-10-08 | [e8cc9c763](https://github.com/Tencent/WeKnora/commit/e8cc9c763e639749bab486a0fc291d91c6b09597) | fix(mcp): include shared knowledge bases in endpoint scope (#3848) | 有效变更，按主题归纳 |
| 2026-10-08 | [69a7fd993](https://github.com/Tencent/WeKnora/commit/69a7fd9931a0867dd7e1003262b315a330e5d606) | fix(mcp): close legacy chat responses on HTTP errors (#3930) | 有效变更，按主题归纳 |
| 2026-10-08 | [c0c194f62](https://github.com/Tencent/WeKnora/commit/c0c194f6253ba38ddfc868767bda21fd6f4ea900) | fix(im/wecom): preserve image in mixed messages with text captions (#3901) | 有效变更，按主题归纳 |
| 2026-10-08 | [95691daab](https://github.com/Tencent/WeKnora/commit/95691daab2e292a8c45e0ae200737b1c9e5de222) | fix(knowledge): retry document processing after transient indexing failures (#3915) | 有效变更，按主题归纳 |
| 2026-10-08 | [fac9ebabf](https://github.com/Tencent/WeKnora/commit/fac9ebabf2d4578f6192528b1e68f69af48d9a96) | fix(knowledge): close spans left open on terminal rows and superseded attempts (#3855) | 有效变更，按主题归纳 |
| 2026-10-08 | [4d7e66312](https://github.com/Tencent/WeKnora/commit/4d7e663128e70f4f4ba496f062fc5a7c23633358) | fix(wiki): preserve pending work when source lookups fail (#3715) | 有效变更，按主题归纳 |
| 2026-10-08 | [626185a12](https://github.com/Tencent/WeKnora/commit/626185a120ef55a741c5add46147c0af146c18aa) | fix(wiki): 重建链接逐页写失败不再静默，半成品不再回 200 (#3884) | 有效变更，按主题归纳 |
| 2026-10-08 | [11bfacad9](https://github.com/Tencent/WeKnora/commit/11bfacad97c7748542a988717bd3f4ffc53244b4) | chore(deps): bump the cli-deps group in /cli with 2 updates (#3920) | 构建/测试/文档或体验调整 |
| 2026-10-08 | [3206e15e8](https://github.com/Tencent/WeKnora/commit/3206e15e831672f0ae0d837d822150ca19415594) | fix(docs): make `make docs` reproduce the committed Swagger again (#3751) | 有效变更，按主题归纳 |
| 2026-10-08 | [9d4cc233c](https://github.com/Tencent/WeKnora/commit/9d4cc233c68a3f3e7ee7fd81926708dd5c898b36) | feat(ui): add search to Skills and MCP toolbox lists (#3731) | 有效变更，按主题归纳 |
| 2026-10-08 | [4ea94afcb](https://github.com/Tencent/WeKnora/commit/4ea94afcb91b7f2388d9440495f30cf555e88a5b) | fix(cli): propagate paginated response read errors (#3983) | 有效变更，按主题归纳 |
| 2026-10-08 | [4a0c7fa91](https://github.com/Tencent/WeKnora/commit/4a0c7fa91f2f646e6f56181ba723a9eebf368ed9) | fix(cli): render paginated API results in text mode (#3984) | 有效变更，按主题归纳 |
| 2026-10-08 | [7d75ce3a6](https://github.com/Tencent/WeKnora/commit/7d75ce3a6f871ae59668b440439ea27a9959c351) | fix(client): make multipart uploads replayable (#3989) | 有效变更，按主题归纳 |
| 2026-10-08 | [cefd5920f](https://github.com/Tencent/WeKnora/commit/cefd5920f885b604cc760478e9dd87b5778a272e) | fix(cli): apply list limits after filtering and sorting (#3990) | 有效变更，按主题归纳 |
| 2026-10-08 | [ff3318443](https://github.com/Tencent/WeKnora/commit/ff33184433c438eea0ec40293289b819fa817c97) | fix(cli): preserve integer precision in jq output (#3991) | 有效变更，按主题归纳 |
| 2026-10-08 | [b413023d2](https://github.com/Tencent/WeKnora/commit/b413023d2885742048103d4d617922465788c0f2) | fix(cli): classify wrapped SDK HTTP errors correctly (#3992) | 有效变更，按主题归纳 |
| 2026-10-08 | [562127c65](https://github.com/Tencent/WeKnora/commit/562127c6542d9f3f073f390658cb67ae65b97484) | fix(client): accept optional spaces in SSE event fields (#3975) | 有效变更，按主题归纳 |
| 2026-10-08 | [b8d83eab1](https://github.com/Tencent/WeKnora/commit/b8d83eab1cf426d19c8e0183b9d896b77a62872b) | fix(skills): skip vendored dep trees in install verification script enumeration (#3943) | 有效变更，按主题归纳 |
| 2026-10-08 | [dad81cb8c](https://github.com/Tencent/WeKnora/commit/dad81cb8c10c52efd5d70dfe1abcc9f0c5672c99) | fix(sandbox): reclassify exec races with the idle sweep as NotFound so rebinding heals (#3980) | 有效变更，按主题归纳 |
| 2026-10-08 | [20038cbda](https://github.com/Tencent/WeKnora/commit/20038cbda6e3c810b4ab3951d56bbcf8f81af19c) | fix(asynqdl): truncate dead-letter errors on a rune boundary (#3964) | 有效变更，按主题归纳 |
| 2026-10-08 | [6057a8101](https://github.com/Tencent/WeKnora/commit/6057a81017375a8d680cd32afca1c2ff03b0b17c) | fix(middleware): stop buffering whole request bodies in the access log (#3927) | 有效变更，按主题归纳 |
| 2026-10-08 | [5077f3580](https://github.com/Tencent/WeKnora/commit/5077f3580003bcf55eeb89694d43479ca216adae) | fix(notion): 超过 block 上限截断时补 Warn，避免内容与子页面静默缺失 (#3771) | 有效变更，按主题归纳 |
| 2026-10-08 | [dabbdaf15](https://github.com/Tencent/WeKnora/commit/dabbdaf156b8dfcb58dd68e9c0be3842d27d72b5) | fix(notion): 分页循环补重复游标防护与跳数上限 (#3890) | 有效变更，按主题归纳 |
| 2026-10-08 | [e7aaf8ae0](https://github.com/Tencent/WeKnora/commit/e7aaf8ae02608d1d9e0f84aa864106a5f1ae78a3) | fix(docreader): keep the space held by a whitespace-only inline element (#3978) | 有效变更，按主题归纳 |
| 2026-10-08 | [336f84c3b](https://github.com/Tencent/WeKnora/commit/336f84c3be4a4831c49b2708f98431dae2ab3312) | fix(data-analysis): skip style-only worksheets before DuckDB loading (#3660) | 有效变更，按主题归纳 |
| 2026-10-08 | [4dd19b535](https://github.com/Tencent/WeKnora/commit/4dd19b535603d4a9dc8b64033192592572db5180) | fix(frontend): render single tilde literally instead of GFM strikethrough (#3963) | 有效变更，按主题归纳 |
| 2026-10-08 | [4eb23a856](https://github.com/Tencent/WeKnora/commit/4eb23a85607c0cabfa050f5e87eb71fb0ce2554c) | fix(qdrant): CopyIndices 用服务端返回的 next_page_offset 翻页，不再按页尾 id 回填 (#3882) | 有效变更，按主题归纳 |
| 2026-10-08 | [e6ecb9adc](https://github.com/Tencent/WeKnora/commit/e6ecb9adcc8ddbb500957f77490ca5cb4c2fa8d4) | fix(dataset): allow selecting the QA answer model (#3944) | 有效变更，按主题归纳 |
| 2026-10-08 | [49e262147](https://github.com/Tencent/WeKnora/commit/49e262147ca504a28680930930ed0851a622d0c5) | fix(cli): preserve directory structure in doc upload --recursive (#3753) | 有效变更，按主题归纳 |
| 2026-10-08 | [de2dfd303](https://github.com/Tencent/WeKnora/commit/de2dfd3034a7b506fa269910401bbdcbd4efc71d) | fix(multimodal): bound remote image downloads by the file size limit (#3906) | 有效变更，按主题归纳 |
| 2026-10-08 | [cbfd66fd9](https://github.com/Tencent/WeKnora/commit/cbfd66fd922df3632448e0bfd5925a6c10196fc8) | fix(knowledge): delete written chunks when the storage quota check fails (#3656) | 有效变更，按主题归纳 |
| 2026-10-08 | [11aa00f85](https://github.com/Tencent/WeKnora/commit/11aa00f85f22ee54a6c37963010ac3f4d20d694b) | fix(agent): preserve untouched numeric tool arguments during casting (#3936) | 有效变更，按主题归纳 |
| 2026-10-08 | [9a2cd2006](https://github.com/Tencent/WeKnora/commit/9a2cd200637359081d87fb4c51b6bc911a0ff6a2) | fix(frontend): keep target="_blank" on sanitized markdown links (#3818) | 有效变更，按主题归纳 |
| 2026-10-08 | [a75e8e9ac](https://github.com/Tencent/WeKnora/commit/a75e8e9ac0ac10ea23c3108e789a8d71f6f071e9) | fix(datasource): 语雀/Notion/IMA/飞书连接器 API 响应体补体积上限 (#3985) | 有效变更，按主题归纳 |
| 2026-10-08 | [b5fd79edf](https://github.com/Tencent/WeKnora/commit/b5fd79edf283f4ac6a272403f231c3c07db11500) | fix(model): treat 400 as reachable in chat connection test again (#3929) | 有效变更，按主题归纳 |
| 2026-10-08 | [1826f46b1](https://github.com/Tencent/WeKnora/commit/1826f46b1217867fe15f188706a9334cacec4165) | fix(docparser): 云解析超时改为跟随既有配置，不再硬编码 (#3891) | 有效变更，按主题归纳 |
| 2026-10-08 | [99b4c3a9b](https://github.com/Tencent/WeKnora/commit/99b4c3a9b0d3c2d3440036c6e3f4235c6f276390) | feat(rerank): support Hugging Face TEI protocol (#3956) | 有效变更，按主题归纳 |
| 2026-10-08 | [d84cdabac](https://github.com/Tencent/WeKnora/commit/d84cdabac49046dc546ba04716afcc3785240a88) | fix(rss): refresh titles and source links when article text is unchanged (#3824) | 有效变更，按主题归纳 |
| 2026-10-08 | [bea0d7b68](https://github.com/Tencent/WeKnora/commit/bea0d7b6891ccbf94d3e1c6f649a3a6092978a12) | fix(docreader): preserve PPTX fallback image associations (#3973) | 有效变更，按主题归纳 |
| 2026-10-08 | [3fb6b9d63](https://github.com/Tencent/WeKnora/commit/3fb6b9d631d399104778cf8d4a79facd23ed8853) | fix(kb): let the update API set vlm_config (#3704) | 有效变更，按主题归纳 |
| 2026-10-08 | [b5c8d90cc](https://github.com/Tencent/WeKnora/commit/b5c8d90cc20747be3f64356150ca69224d063be0) | fix(chat_pipeline): treat non-JSON model refusals as skipped graph extraction (#3606) | 有效变更，按主题归纳 |
| 2026-10-08 | [ae9054626](https://github.com/Tencent/WeKnora/commit/ae90546262a84dcfd90b5da370b56bd0774cc8f2) | fix(frontend): avoid false unsaved changes warnings | 有效变更，按主题归纳 |
| 2026-10-08 | [bcbd65a45](https://github.com/Tencent/WeKnora/commit/bcbd65a459e912f70ad4a9b0f39f8848db4a2d9f) | fix(datasource/dingtalk): Validate 不再因为某个工作区读不到就整库判失败 (#3782) | 有效变更，按主题归纳 |
| 2026-10-08 | [19542f2a1](https://github.com/Tencent/WeKnora/commit/19542f2a1d958d26383bbb586adecae6be07a7d5) | feat(datasource): add Seafile data source connector (#3616) | 有效变更，按主题归纳 |
| 2026-10-08 | [d2bc0df6a](https://github.com/Tencent/WeKnora/commit/d2bc0df6a2af4f953485e69c63972cf7d1cc2646) | feat(i18n): add Traditional Chinese (zh-TW) locale support (#3916) | 有效变更，按主题归纳 |
| 2026-10-08 | [1c528d83d](https://github.com/Tencent/WeKnora/commit/1c528d83de39d79e870fe8b46d21addf20136a60) | fix(i18n): add the zh-TW Seafile strings missing after #3616 and #3916 | 有效变更，按主题归纳 |
| 2026-10-08 | [0de62b2f8](https://github.com/Tencent/WeKnora/commit/0de62b2f801513982291311b59fad381f7959560) | fix(chat): restore citation source navigation and soften highlights | 有效变更，按主题归纳 |
| 2026-10-08 | [a729d1fc8](https://github.com/Tencent/WeKnora/commit/a729d1fc8c1c7e05119b69696ef9f61bc2627cfb) | docs(models): state which rerank limits split requests and which fail the call | 构建/测试/文档或体验调整 |
| 2026-10-08 | [ea7cf8d5d](https://github.com/Tencent/WeKnora/commit/ea7cf8d5d77cf0deb46215836262405ad91d4b44) | fix(models): emit explicit empty content for assistant tool-call turns (#3605) | 有效变更，按主题归纳 |
| 2026-10-08 | [5a25e2614](https://github.com/Tencent/WeKnora/commit/5a25e261421db4c76b720e9d7392bd8f88f36585) | feat(rerank): support Pinecone Inference protocol (#3957) | 有效变更，按主题归纳 |
| 2026-10-08 | [b7f318106](https://github.com/Tencent/WeKnora/commit/b7f31810664e22e44cb6da2dae3b2115c2060f49) | docs: count 29 built-in model vendors and list Hugging Face TEI and Pinecone (#4048) | 构建/测试/文档或体验调整 |
| 2026-10-08 | [0c5d28abb](https://github.com/Tencent/WeKnora/commit/0c5d28abb7917b49d36f79bafb78fb59796a068b) | feat: index and recall images with multimodal embedding models (#3683) | 有效变更，按主题归纳 |
| 2026-10-08 | [676a59307](https://github.com/Tencent/WeKnora/commit/676a59307e5ca7efabfe8896354b9425703aef67) | fix(settings): improve modal sizing and integration page layout | 有效变更，按主题归纳 |
| 2026-10-08 | [2a02807a6](https://github.com/Tencent/WeKnora/commit/2a02807a618a18196ebc1ddbe9888fb742b24117) | fix(chunker): respect Markdown fences during heading detection | 有效变更，按主题归纳 |
| 2026-10-08 | [005627b18](https://github.com/Tencent/WeKnora/commit/005627b180c6c7509660b0a14401e463f48d53f8) | fix(agent): compare tool enum values without string coercion | 有效变更，按主题归纳 |
| 2026-10-09 | [b14bcbcd5](https://github.com/Tencent/WeKnora/commit/b14bcbcd541fc0fdd67eef518828a28302358e18) | fix(notion): 保留正文抓取失败时的同步游标 (#3693) | 有效变更，按主题归纳 |
| 2026-10-09 | [04da26024](https://github.com/Tencent/WeKnora/commit/04da26024c2412b34b3b30e7b283c98197ef95f1) | fix(weaviate): CopyIndices 补游标防护，并修掉 GraphQL 报错时的 panic (#3881) | 有效变更，按主题归纳 |
| 2026-10-09 | [5d40f81ee](https://github.com/Tencent/WeKnora/commit/5d40f81ee4020d8f3173ac64df4dc4ae140351c1) | fix(im): retry Yunzhijia attachment downloads (#3809) | 有效变更，按主题归纳 |
| 2026-10-09 | [419f17fc6](https://github.com/Tencent/WeKnora/commit/419f17fc651de93a34eb3586cb894a9e7f3db4a4) | fix(wiki): resolve titles for unloaded backlinks (#3931) | 有效变更，按主题归纳 |
| 2026-10-09 | [cbfc0fed4](https://github.com/Tencent/WeKnora/commit/cbfc0fed4a1cb4a3ff0ffabe943d6f1edfa3b21e) | fix(chat): 在模型上下文中标注 FILTER_TOP_K 的子集截断，避免把前 N 条当作全集作答 (#3758) | 有效变更，按主题归纳 |
| 2026-10-09 | [abc365f91](https://github.com/Tencent/WeKnora/commit/abc365f919191317020d395e9af19e4129357ff3) | fix(wiki): 唯一写出口 UpdatePage 拦住丢表格行的机器重写（agent 整页写入不再静默改短页面） (#3793) | 有效变更，按主题归纳 |
| 2026-10-09 | [8319d59dd](https://github.com/Tencent/WeKnora/commit/8319d59dd014fccb7daf04eed9d50d8a3ffdecf4) | feat(datasource/dingtalk): 摄取钉钉"上传文件"（docx/pptx/xlsx/pdf） (#3786) | 有效变更，按主题归纳 |
| 2026-10-09 | [09411106f](https://github.com/Tencent/WeKnora/commit/09411106f0ec19e840e9c24cabf391b8c2852a8e) | fix(session): allow tenant admins to manage channel sessions (#3939) | 有效变更，按主题归纳 |
| 2026-10-09 | [3d01bebce](https://github.com/Tencent/WeKnora/commit/3d01bebce3d7aa90aaa52914534f22d9cadf683b) | fix(docreader): resolve EPUB images relative to their chapters (#3924) | 有效变更，按主题归纳 |
| 2026-10-09 | [4243018ff](https://github.com/Tencent/WeKnora/commit/4243018ff1f55ccd92baebf4da0a1057317cc644) | fix(mcp): report advertised directory failures in connection tests (#3905) | 有效变更，按主题归纳 |
| 2026-10-09 | [a1450d382](https://github.com/Tencent/WeKnora/commit/a1450d382e39a06977cc395aaf822eebab87c69e) | fix(knowledge): claim cloned and released document images in the resource catalog (#3752) | 有效变更，按主题归纳 |
| 2026-10-09 | [65f88f412](https://github.com/Tencent/WeKnora/commit/65f88f4121433702ea053ae9127647329bf98b05) | feat: judge image hits by their image in rerank and show them to vision models (#4013) | 有效变更，按主题归纳 |
| 2026-10-09 | [52617f641](https://github.com/Tencent/WeKnora/commit/52617f64106e2ea9af770ff783a1818e1ce7b8fb) | fix(sandbox): omit env overrides when creating Aliyun E2B sandboxes from snapshots (#4054) | 有效变更，按主题归纳 |
| 2026-10-09 | [602d0c388](https://github.com/Tencent/WeKnora/commit/602d0c388bf17d894f467fe0cd65d1d62c73d303) | feat: support app and docreader build download mirrors (#4073) | 有效变更，按主题归纳 |
| 2026-10-10 | [d074ec7b0](https://github.com/Tencent/WeKnora/commit/d074ec7b0b63903f117543e09deb31f5f3d19ecb) | fix(websearch): return results for non-webpage Metaso scopes (#4010) | 有效变更，按主题归纳 |
| 2026-10-10 | [40eb34bc8](https://github.com/Tencent/WeKnora/commit/40eb34bc849cb29dc53ccddef971c9a7c4410f7b) | fix(agent): align web_search count limit with the configured maximum (#4011) | 有效变更，按主题归纳 |
| 2026-10-10 | [c3f077ed1](https://github.com/Tencent/WeKnora/commit/c3f077ed18f6e00aaf705d405023ed88c7c15b27) | fix(models): align configuration and multimodal embedding workflows (#4080) | 有效变更，按主题归纳 |
| 2026-10-10 | [0c71fff2a](https://github.com/Tencent/WeKnora/commit/0c71fff2a387b539c449cd6b66a3922ed6839f76) | fix(sandbox): treat E2B in_progress template builds as building (#4108) | 有效变更，按主题归纳 |
| 2026-10-10 | [6e2e5d8bb](https://github.com/Tencent/WeKnora/commit/6e2e5d8bb07cd9c8db360c03b80ca47ea2e81fd5) | fix(frontend): follow configured upload limit in hints (#4110) | 有效变更，按主题归纳 |
| 2026-10-10 | [097bd475f](https://github.com/Tencent/WeKnora/commit/097bd475f601d48a82ed7aaa2f9676dd82f15d97) | fix(docker): install CA certificates before the browserskill mirror swap (#4104) | 有效变更，按主题归纳 |
| 2026-10-10 | [8125ed250](https://github.com/Tencent/WeKnora/commit/8125ed250f5ea404445e7952359a6ac686d90afc) | fix(chat): keep custom metadata on knowledge-chat references (#4087) | 有效变更，按主题归纳 |
| 2026-10-10 | [d6269994b](https://github.com/Tencent/WeKnora/commit/d6269994b33688c2b85488c24014d95db1948d52) | refactor: 出站拨号超时默认 5s 并改为 env 可配 (WEKNORA_OUTBOUND_DIAL_TIMEOUT) (#4122) | 有效变更，按主题归纳 |
| 2026-10-10 | [03e2c47ae](https://github.com/Tencent/WeKnora/commit/03e2c47aecbf60120820216baaa8abb2e98844f7) | feat(mcp): support 2026-07-28 alongside legacy clients (#4095) | 有效变更，按主题归纳 |
| 2026-10-10 | [e372b8e42](https://github.com/Tencent/WeKnora/commit/e372b8e42ef71f8ba9e1c4ada6546d79ec056f91) | perf(embedding): batch Qwen3-VL message inputs (#4126) | 有效变更，按主题归纳 |
| 2026-10-10 | [8cb53fdbc](https://github.com/Tencent/WeKnora/commit/8cb53fdbc58e4c15aafdba973d65742551e4c9cd) | fix(observability): distinguish retrieval diagnostic spans (#4128) | 有效变更，按主题归纳 |
| 2026-10-10 | [bb878af50](https://github.com/Tencent/WeKnora/commit/bb878af50873b919a68134dc007ba9ae859c6c39) | fix(gallery): improve image detail panel readability (#4130) | 有效变更，按主题归纳 |
| 2026-10-10 | [d27ecade6](https://github.com/Tencent/WeKnora/commit/d27ecade656a1f2335baa2520d2622a297036ea5) | fix(ocr): reject invalid output and record extraction failures (#4132) | 有效变更，按主题归纳 |
| 2026-10-10 | [5f18308b0](https://github.com/Tencent/WeKnora/commit/5f18308b0a39bd44f3b44203c167663d0430a40b) | fix(ocr): reject split repetition and preserve no-text responses (#4135) | 有效变更，按主题归纳 |
| 2026-10-10 | [a58fc1320](https://github.com/Tencent/WeKnora/commit/a58fc13206c7d1e89c784d61189ab67295727744) | fix(chat): smooth streamed citation reveal cadence | 有效变更，按主题归纳 |
| 2026-10-10 | [aa535c22f](https://github.com/Tencent/WeKnora/commit/aa535c22fed1f4ba5c965b7f208361c0b1c2d4e2) | fix(chat): keep queued follow-up answers attached to their own question | 有效变更，按主题归纳 |
| 2026-10-10 | [50a296316](https://github.com/Tencent/WeKnora/commit/50a29631608b9b95c88b4ea98e09867bdbcad991) | fix(preview): refine source citation highlights (#4139) | 有效变更，按主题归纳 |
| 2026-10-10 | [d8fa5b49a](https://github.com/Tencent/WeKnora/commit/d8fa5b49a7f4aa93ded9978c40d1b57346375211) | feat(embed): support pasting screenshots into chat | 有效变更，按主题归纳 |
| 2026-10-10 | [98e9dc078](https://github.com/Tencent/WeKnora/commit/98e9dc078625265ba1ea85ee3538a547f85a5be0) | perf(reranking): accelerate MMR selection with sparse postings | 有效变更，按主题归纳 |
| 2026-10-10 | [54e8d7ffd](https://github.com/Tencent/WeKnora/commit/54e8d7ffde31ccc00ea59cec7b2eaec18b01f137) | fix(mcp): 隔离慢连接清理对其他服务的阻塞 | 有效变更，按主题归纳 |
| 2026-10-10 | [dccb1d6d2](https://github.com/Tencent/WeKnora/commit/dccb1d6d2c974062976e8b8841c46bbddb06d226) | fix(mcp): stop advertising read-only endpoint tools as destructive | 有效变更，按主题归纳 |
| 2026-10-10 | [de616ed4b](https://github.com/Tencent/WeKnora/commit/de616ed4b7d2a6e55c9d380bd6068f792fe9f3a1) | test(service): assert RRF ranks ignore list arrival order | 构建/测试/文档或体验调整 |
| 2026-10-10 | [1a21af1b8](https://github.com/Tencent/WeKnora/commit/1a21af1b827072589eddcc9def0b91433399480c) | fix(mcp): preserve structured tool results for agents | 有效变更，按主题归纳 |
| 2026-10-10 | [d9237166c](https://github.com/Tencent/WeKnora/commit/d9237166cc4cb618818d2e65e62f820f1cc03f24) | fix(im/wecom): accept custom wss long-connection endpoints behind SSRF host checks | 有效变更，按主题归纳 |
| 2026-10-10 | [def582b98](https://github.com/Tencent/WeKnora/commit/def582b98a2960ceb53d7051f27b90014c08858f) | test(im/wecom): satisfy errcheck and lll in endpoint tests | 构建/测试/文档或体验调整 |
| 2026-10-10 | [769a5aeca](https://github.com/Tencent/WeKnora/commit/769a5aeca84331b1f1f5ad5f3b93934ec33d4fbf) | fix(frontend): preserve the caret when outdenting Markdown lists | 有效变更，按主题归纳 |
| 2026-10-10 | [57f8a65fe](https://github.com/Tencent/WeKnora/commit/57f8a65fec7b86fb20aa2c19a0e21b67a3864c73) | fix(datasource/dingtalk): tighten Validate sampling edges (#4079) | 有效变更，按主题归纳 |
| 2026-10-10 | [932b750b2](https://github.com/Tencent/WeKnora/commit/932b750b2f5ef1ce2537ac57a117dec7423f46ab) | fix(agent): preserve steering order across persistence failures (#4078) | 有效变更，按主题归纳 |
| 2026-10-10 | [0466c294e](https://github.com/Tencent/WeKnora/commit/0466c294ef1ad728e889058d331a5cfc37b2bd23) | fix(agent): 将空闲超时覆盖到模型流建立阶段 | 有效变更，按主题归纳 |
| 2026-10-10 | [3f9af2afa](https://github.com/Tencent/WeKnora/commit/3f9af2afa912e16ebaec1ae172963a99dfc2e419) | fix(sqlite): add MCP metadata migration | 有效变更，按主题归纳 |
| 2026-10-10 | [45a022aac](https://github.com/Tencent/WeKnora/commit/45a022aac23448e270f23046c9018cf8b2e37761) | fix: wait for qdrant writes to finish | 有效变更，按主题归纳 |
| 2026-10-10 | [89e3273da](https://github.com/Tencent/WeKnora/commit/89e3273daf3c11f1a4498183b75c8257efd8db6c) | fix(mcp): preserve disabled state on service creation | 有效变更，按主题归纳 |
| 2026-10-10 | [0d39af121](https://github.com/Tencent/WeKnora/commit/0d39af1214ff9d02382a4e851d320f5be8957985) | fix(retrieval): skip KB retrieval for summarize intent | 有效变更，按主题归纳 |
| 2026-10-10 | [a6327f8e3](https://github.com/Tencent/WeKnora/commit/a6327f8e39fe62b299348e16a9bc81763c0fcf00) | fix(i18n): correct Korean username rule message to match the pattern | 有效变更，按主题归纳 |
| 2026-10-10 | [b1d92048c](https://github.com/Tencent/WeKnora/commit/b1d92048cf61d732a6cd9171237c8d05770183e1) | fix(agent): account for file paths in read pagination budgets | 有效变更，按主题归纳 |
| 2026-10-10 | [54cb6e9f9](https://github.com/Tencent/WeKnora/commit/54cb6e9f9be8cf486bb88b62916f13e16351f9a5) | test(docreader): add unit tests for utils.split text-splitting helpers | 构建/测试/文档或体验调整 |
| 2026-10-10 | [150d8cc60](https://github.com/Tencent/WeKnora/commit/150d8cc605ed9305a5fc4ad1b8789dae238decce) | feat(sandbox): Docker 后端支持交互式终端 (#4023) | 有效变更，按主题归纳 |
| 2026-10-11 | [231a5c0b1](https://github.com/Tencent/WeKnora/commit/231a5c0b15b817bb13ee3a876c3fbaf337cb4d5e) | feat(wiki): improve graph layout, theme captions and group focus (#4147) | 有效变更，按主题归纳 |
| 2026-10-11 | [d2dbf7209](https://github.com/Tencent/WeKnora/commit/d2dbf7209963f0a9f6fafb70358ef9baec999aa3) | style: polish sidebar, list pages, toolbox and citation UI (#4154) | 构建/测试/文档或体验调整 |
| 2026-10-11 | [17dc7da51](https://github.com/Tencent/WeKnora/commit/17dc7da514e1fd86f6c2fe77ab05c799cf34c27a) | feat(gallery): replace pagination with infinite masonry scrolling (#4156) | 有效变更，按主题归纳 |
| 2026-10-11 | [643f64180](https://github.com/Tencent/WeKnora/commit/643f64180eb1898a7a2e32d517ff73f978d5448f) | feat(system): add platform user management and streamline settings (#4153) | 有效变更，按主题归纳 |
| 2026-10-11 | [5343ac47b](https://github.com/Tencent/WeKnora/commit/5343ac47b730d451ad267004530084e08a5f599b) | fix(models): stop sending temperature to Claude models that reject it (#4155) | 有效变更，按主题归纳 |
| 2026-10-11 | [00474f734](https://github.com/Tencent/WeKnora/commit/00474f734d6c7c0911d7485465d7c7286d0047cb) | fix: align settings layouts and tolerate existing migration objects (#4164) | 有效变更，按主题归纳 |
| 2026-10-12 | [9efe94cc6](https://github.com/Tencent/WeKnora/commit/9efe94cc6f6d623590098f1fb355dd1f2e63b8f5) | feat(models): add Claude Opus 5.5, Sonnet 5.5 and Haiku 5.5 to the catalog (#4158) | 有效变更，按主题归纳 |
| 2026-10-12 | [d272a83cf](https://github.com/Tencent/WeKnora/commit/d272a83cfc5d4a92aa3655f6a6c806aa40a34c70) | fix(deploy): pass WEKNORA_OUTBOUND_DIAL_TIMEOUT through to the app container (#4157) | 有效变更，按主题归纳 |
| 2026-10-12 | [be581c41e](https://github.com/Tencent/WeKnora/commit/be581c41e0f17db25fd50493c345a5c43a8db138) | fix(mcp): close only the authorizing user's connection after OAuth (#4138) | 有效变更，按主题归纳 |
| 2026-10-12 | [21ef506fc](https://github.com/Tencent/WeKnora/commit/21ef506fc995259e48680477ce8eb06556bbd957) | fix(agent): scope database queries to authorized shared knowledge bases (#4136) | 有效变更，按主题归纳 |
| 2026-10-12 | [3c36cb19a](https://github.com/Tencent/WeKnora/commit/3c36cb19a2bb4214835cf8e5d520d4aa64ac8f23) | fix(deploy): bump paradedb image to v0.25.11-pg17 (#4120) | 有效变更，按主题归纳 |
| 2026-10-12 | [c6bfe8189](https://github.com/Tencent/WeKnora/commit/c6bfe81894be411c910ad573bd1c80d88452414b) | feat(helm): allow Service name overrides with matching consumer references (#4109) | 有效变更，按主题归纳 |
| 2026-10-12 | [e5f9f9dea](https://github.com/Tencent/WeKnora/commit/e5f9f9deab36898676fe85a9fda6b3e6f00288fa) | feat(rerank): add Cohere provider for the v2 rerank API | 有效变更，按主题归纳 |
| 2026-10-12 | [fe444c851](https://github.com/Tencent/WeKnora/commit/fe444c851ef4430bcdcbef4cb10342839c57807f) | fix(feishu): add shared request pacing and improve HTTP 429 / error 99991400 retries in Feishu Wiki synchronization (#4039) | 有效变更，按主题归纳 |
| 2026-10-12 | [24b5ca3f2](https://github.com/Tencent/WeKnora/commit/24b5ca3f2325e28acb4122384459ca66acc85ef4) | docs: clarify frontend development proxy configuration (#4171) | 构建/测试/文档或体验调整 |
| 2026-10-12 | [7925f774d](https://github.com/Tencent/WeKnora/commit/7925f774d1baa72e8244b5fc65324542049677a8) | fix(datasource/gitlab): emptied files no longer leave their old body searchable (#4008) | 有效变更，按主题归纳 |
| 2026-10-12 | [9b031a657](https://github.com/Tencent/WeKnora/commit/9b031a657656cddaddafe3cb2e124ccfc2f0693e) | fix: accept qdrant collection created concurrently (#3999) | 有效变更，按主题归纳 |
| 2026-10-12 | [7982c6a3b](https://github.com/Tencent/WeKnora/commit/7982c6a3bda4b91da402faf39ca381cabd4832ca) | fix(agent): accept adjacent prefixed Python string literals (#4006) | 有效变更，按主题归纳 |
| 2026-10-12 | [6a7094cea](https://github.com/Tencent/WeKnora/commit/6a7094ceacd3d9844ab9f2ad9083075e1f7cd088) | fix(mcp): bound resource listing pagination (#4007) | 有效变更，按主题归纳 |
| 2026-10-12 | [ecdcd14e1](https://github.com/Tencent/WeKnora/commit/ecdcd14e1db5452e929ea846b0a1677502f81464) | docs: refresh LICENSE third-party notices (#4178) | 构建/测试/文档或体验调整 |
| 2026-10-12 | [c2edec192](https://github.com/Tencent/WeKnora/commit/c2edec192b5b4abbe77c0e210b3e5abfb51792cc) | docs: explain knowledge upload folder paths (#4177) | 构建/测试/文档或体验调整 |
| 2026-10-12 | [089711f92](https://github.com/Tencent/WeKnora/commit/089711f92c50ecad782ab95dbce8b04278a9cb35) | fix(mcp): ignore discover probe in resource list tests (#4179) | 有效变更，按主题归纳 |
| 2026-10-12 | [98362dd29](https://github.com/Tencent/WeKnora/commit/98362dd29698f4b78df5d8ecf3f5020517e50ddc) | feat(gallery): filter images by source document (#4159) | 有效变更，按主题归纳 |
| 2026-10-12 | [1c54beb55](https://github.com/Tencent/WeKnora/commit/1c54beb551673f15c6054ffb7f224a4bfdb495e3) | refactor(gallery): turn source document filter into a toolbar popover (#4190) | 有效变更，按主题归纳 |
| 2026-10-12 | [ef54e835c](https://github.com/Tencent/WeKnora/commit/ef54e835c907718cedf7316bfa93daf69a28b2ba) | docs(api): clarify knowledge-search scope union of knowledge_base_ids and knowledge_ids (#4191) | 构建/测试/文档或体验调整 |
| 2026-10-12 | [8fae51c64](https://github.com/Tencent/WeKnora/commit/8fae51c64084a5cc938eadb32d27c61eea4f4d87) | style(frontend): polish knowledge base settings hints and sub-sections (#4193) | 构建/测试/文档或体验调整 |
| 2026-10-12 | [fd4cc4584](https://github.com/Tencent/WeKnora/commit/fd4cc4584884f7d21d8949d40de5404aa6b14a40) | fix(frontend): respect Markdown indentation selection boundaries (#4195) | 有效变更，按主题归纳 |
| 2026-10-12 | [97796c637](https://github.com/Tencent/WeKnora/commit/97796c637aa53713edc846bd0a39470d687f0fdf) | fix(helm): configure app rollouts and document shared storage (#4180) | 有效变更，按主题归纳 |
| 2026-10-13 | [809bda7b2](https://github.com/Tencent/WeKnora/commit/809bda7b2f2d44952eaa8765d91e3eb666a42c1c) | fix(agent): make agent tool selection match the tools the runtime registers (#4211) | 有效变更，按主题归纳 |
| 2026-10-13 | [194bffbb0](https://github.com/Tencent/WeKnora/commit/194bffbb0a2b07a9a4bcf35355d18ef56dc6535b) | feat(agent): read chat attachments on demand (#4203) | 有效变更，按主题归纳 |
| 2026-10-13 | [e0712df3e](https://github.com/Tencent/WeKnora/commit/e0712df3e020c3b9744a333cfa102b5a6e6371f7) | feat(mcp): let callers without a browser obtain MCP OAuth tokens (#4229) | 有效变更，按主题归纳 |
| 2026-10-13 | [ec382d791](https://github.com/Tencent/WeKnora/commit/ec382d79114cef327da3f857004077f725da110c) | fix(frontend): preserve inline images in EPUB previews (#4226) | 有效变更，按主题归纳 |
| 2026-10-13 | [a7a5f4762](https://github.com/Tencent/WeKnora/commit/a7a5f47625f31698897a62c152b1e186c95b43c7) | fix(frontend): preserve nonempty lists when pressing Enter (#4224) | 有效变更，按主题归纳 |
| 2026-10-13 | [7972cdd25](https://github.com/Tencent/WeKnora/commit/7972cdd251acdb599eb41dcb66ad312982f82360) | fix(knowledge): make the knowledge base description token budget configurable (#4230) | 有效变更，按主题归纳 |
| 2026-10-13 | [621bc9b98](https://github.com/Tencent/WeKnora/commit/621bc9b98ad19b32f553da83f6cbb14f7f74a4d6) | fix(website-docs): prebundle fastdom when mermaid imports it (#4208) | 有效变更，按主题归纳 |
| 2026-10-13 | [8af7938ae](https://github.com/Tencent/WeKnora/commit/8af7938aeddfaaf55fb0578817482a3468f2a045) | fix(mcp): keep OAuth token expiry independent of the process time zone (#4207) | 有效变更，按主题归纳 |
| 2026-10-13 | [94fae6de4](https://github.com/Tencent/WeKnora/commit/94fae6de43a36e44d64aecbbdf19c0b72b352aa8) | feat(models): add WeMM embedding presets and Transformers serving (#4238) | 有效变更，按主题归纳 |
| 2026-10-13 | [4356f8c16](https://github.com/Tencent/WeKnora/commit/4356f8c16dcd5cae85652e9afebf63dc280334fc) | fix(skill): do not fail installs on non-blocking observations (#4246) | 有效变更，按主题归纳 |
| 2026-10-13 | [e51dee829](https://github.com/Tencent/WeKnora/commit/e51dee8297e10db8b0037a8e0cf5d684010cea44) | feat(chat): polish rewind confirm and mark sessions with unread replies (#4247) | 有效变更，按主题归纳 |
| 2026-10-13 | [2511a1f93](https://github.com/Tencent/WeKnora/commit/2511a1f932a7a0dc0e9a04edb62986df0b147cca) | fix(mcp): offer OAuth prompt after tool approval and tidy tools panel (#4248) | 有效变更，按主题归纳 |
| 2026-10-13 | [923822086](https://github.com/Tencent/WeKnora/commit/92382208674ae1b9a2dd62ac0fa98d67ce348bfe) | feat(chat): add global search to the @ mention menu and align its styling (#4249) | 有效变更，按主题归纳 |
| 2026-10-13 | [31c605568](https://github.com/Tencent/WeKnora/commit/31c6055687d33f9ad7a92cf453a264ce13a1700d) | feat(agent): name the turn's chat model on the completion event (#4252) | 有效变更，按主题归纳 |
