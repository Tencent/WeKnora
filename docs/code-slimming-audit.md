# 代码瘦身审计与实施记录

审计日期：2026-09-16。基线：`d5efe5ced`。目标：保留现有功能，减少重复维护和理解成本。

## 扫描范围与口径

- 统计 Git 跟踪的源码物理行数，包含注释、空行；分开统计测试、生成文件、翻译与 i18n 辅助代码。行数用于定位，不作为质量评分。
- 对 `frontend/src` 的 TypeScript 和 Vue script 建立静态 import、re-export、字面量动态 import 依赖图，从 `main.ts`、`embed-main.ts` 两个实际入口遍历；额外检查模板名称、路由、全局组件注册、glob 和构建入口。
- 对 `internal` 下 Go 文件进行 AST 扫描，统计函数长度，寻找包内没有其他标识符引用的非导出自由函数；计入测试文件引用。另对重复代码块、相同函数体进行候选扫描并抽查调用方。
- 检查最近 200 个非 merge commit 的文件变更频率，结合重复程度和状态复杂度排序。
- `cli`、`client`、`docreader`、`mcp-server`、`packages`、`miniprogram` 做体量与结构初筛；本轮没有完成这些子项目的逐函数调用审计。
- 依赖图不是运行时覆盖证明；后端候选未经过所有平台、构建标签和集成测试验证，不等同于可直接删除的清单。

基线体量：

| 范围 | 生产源码行数 | 测试行数 |
| --- | ---: | ---: |
| `frontend/src` | 189,597 | 13,930 |
| `internal` | 319,317 | 181,900 |

前端另有 39,411 行翻译/i18n 辅助代码、693 行生成代码。整个仓库的测试和生成代码不应被当作优先削减对象。

## 第一批已实施

### 删除 10 个不可达组件

两个应用入口均无法到达以下组件；仓库内没有引用其文件路径的测试、脚本或注册入口。Vite 未配置组件自动注册，发现的资源 glob 用于 provider SVG，不会加载这些 Vue 文件。

| 文件（相对 `frontend/src`） | 行数 | 核查结果 |
| --- | ---: | --- |
| `views/settings/StorageEngineSettings.vue` | 1,687 | `Settings.vue` 实际导入 `StorageBackendSettings.vue`；同步修正了旧组件别名 |
| `views/organization/OrganizationEditorModal.vue` | 914 | 组织列表实际使用 `OrganizationSettingsModal.vue` |
| `views/settings/components/McpTestResultBody.vue` | 434 | 没有 import、注册或动态加载入口 |
| `components/SourceSwitcherDropdown.vue` | 163 | 没有 import、注册或动态加载入口 |
| `components/settings/SettingCard.vue` | 152 | 仅剩过时注释提及，注释一并清理 |
| `views/organization/JoinOrganization.vue` | 127 | `/join` 已重定向到组织列表，通过 `invite_code` 处理加入 |
| `components/SessionGroupByDropdown.vue` | 123 | 没有 import、注册或动态加载入口 |
| `views/knowledge/settings/KBIndexingStrategy.vue` | 109 | 没有 import、注册或动态加载入口 |
| `views/chat/components/sendMsg.vue` | 22 | 与仍在使用的 `sendMsg` 函数同名，但组件本身没有入口 |
| `views/platform/RoutePlaceholder.vue` | 7 | 没有路由或组件引用 |

共删除 3,738 行失效组件。它们原本不在应用入口依赖链中，因此这是源码维护收益，不能换算为构建产物缩小比例。

### 合并标签选择与创建逻辑

`TagEditDialog.vue`、`BatchTagDialog.vue` 共享新的 `composables/useKnowledgeTagSelection.ts`：

- 选择集合、名称映射、搜索过滤、切换与清空。
- 打开时恢复父组件选中值，重新打开时清空草稿。
- 创建标签的加载状态、结果处理、错误回调；手动输入沿用精确同名复用规则。
- 接口调用和通知通过参数提供，两个组件保留各自提交、关闭和 loading 行为。

两个弹窗的 template 和 style 与基线逐字节一致。新增 7 项行为测试，覆盖父数据更新、缺失标签 ID、搜索、创建响应、失败重试和实例隔离。

第一批生产源码净减少 **3,819 行**，已计入新增共享逻辑，未把新增测试和本文档计入生产源码。

## 第二批已实施：Skill 安装模型配置

基于第一批合并后的 `bfa7f0ed6`，将 `SandboxSkillsPanel.vue` 和 `SkillSettings.vue` 的重复配置逻辑收敛到 `composables/useSkillInstallerModel.ts`。生产源码净减少 40 行。

- 两个页面复用模型读取、最近聊天模型回退、保存、选择器错误通知和 loading 处理；每个实例仍保持独立状态。
- 保存只替换配置中的 `model_id`，保留现有 Agent 元数据与其他配置；读取失败、返回缺少 data、localStorage 不可用时沿用原有回退行为。
- 面板清空配置时调用 `resetInstallerModel`，继续清除已加载 Agent 与模型选择。此方法不增加异步请求取消语义。
- 页面继续控制何时加载、何时安装以及安装前的保存。进度订阅、安装/卸载流程、模板和样式未修改。
- 新增 10 项行为测试，覆盖优先级、回退、配置保留、响应缺省、保存失败重试、错误传播与实例隔离。
- `scripts/verify_frontend_pr.sh` 通过：917 项前端测试、类型检查和生产构建。两个页面的 template/style 与本批基线逐字节一致。未做浏览器视觉验收；未修改或测试后端。

## 第三批已实施：Skill 安装进度订阅

基于第二批合并后的 `5bcc43d72`，面板改为复用 `useConfigSkillInstallProgress`，统一 SSE 请求、配置/Skill 标识、连接去重、取消和完成事件处理。

- 目录页继续只保留所跟踪任务的进度；面板通过 `retainProgress` 保留完成事件与卸载失败原因，显式重试时清空。面板的百分比显示规则和卸载初始进度保持不变。
- 连接回调和 Promise 清理检查当前连接身份，避免旧事件、旧请求拒绝或结束回调覆盖/取消替代连接。正常 EOF 也释放订阅标记，允许后续轮询重新连接。
- 配置切换立即停止原订阅和轮询；面板读取、重试、停止和卸载请求的后续处理检查面板代次，避免迟到结果在另一配置或卸载后重新订阅。
- 新增 11 项行为测试，包含真实面板 setup 的配置切换、卸载失败展示、相同 Skill ID 重试、异步请求迟到和卸载清理；替换原有一项订阅源码正则断言。
- `scripts/verify_frontend_pr.sh` 通过：927 项前端测试、类型检查和生产构建。面板 template/style 与本批基线逐字节一致；未做浏览器视觉验收，未修改或测试后端。

## 第四批已实施：聊天引用浮层

基于第三批合入并同步后的 `42e616398`，`useChatCitationPopover` 与 `useEmbedCitationPopover` 改为复用 `useCitationPopover`。计入新增共享实现后，生产源码净减少 174 行。

- 统一浮层状态、定位、悬停延迟、关闭计时、引用抽屉和事件绑定；两个入口保留 API 选择、缓存作用域和错误展示策略。独立嵌入入口即使凭证为空，也继续只调用嵌入接口。
- 保留主聊天的会话缓存、嵌入的 channel/token 缓存隔离、主聊天滚动/缩放关闭逻辑、嵌入 Wiki 点击拦截，以及两种入口原有的悬停引用 ID 解析差异。
- 绑定对象单独记录，节点替换时移除旧节点监听；根节点、作用域或浮层目标变化后，迟到请求不会覆盖当前内容或写回缓存。卸载时清理监听和计时器。
- 嵌入消息组件直接使用共享关闭计时器，修复触发区与浮层之间无法取消彼此关闭任务的问题；相同节点的流式内容更新不会重置浮层。
- 新增 15 项真实 composable 行为测试，覆盖两个入口、接口路由、缓存、引用抽屉、请求竞争、计时和 Vue 挂载/卸载生命周期。
- `scripts/verify_frontend_pr.sh` 通过：948 项测试通过，1 项原有浏览器场景测试跳过，类型检查和生产构建通过。嵌入消息 template/style 与本批基线逐字节一致；未做浏览器视觉验收，未修改或测试后端。

## 第五批已实施：Go 未引用私有函数

基于第四批合入后的 `d645334d0`，逐包核实原有 16 个候选，全部确认无调用；同时删除仅被候选使用的 `collectTableAliases` 与 `escapeDoubleQuotes`。共移除 18 个私有函数和 3 个失效 import，生产 Go 源码净减少 235 行。

- 使用 Go AST 检查候选所属 12 个包目录中的 894 个 Go 文件，包括测试、带构建标签和平台后缀的源码；删除集合以外没有对应标识符引用。全仓搜索同时检查文档、脚本和特殊链接引用。
- 对修改的生产 Go 文件逐函数比较：453 个保留函数的签名与函数体完全一致。Qdrant 的 `tokenizeQuery`、Agent 工具包的 `formatFileSize`、JSON 解析包的 `formatValue` 等同名活跃实现继续保留。
- 更新分块配置相关注释和开发文档，指向仍在使用的 `buildSplitterConfigFromChunking` / `NormalizeSplitterConfig`；迁移历史注释保留旧名称。
- 13 个受影响包执行 `go test -count=1`：12 个包通过，微信适配包无测试文件但编译通过；3,529 项顶层测试通过、1 项原有测试跳过，计入子测试为 5,274 项通过。跳过项为需要可替换连接探测或真实后端的向量存储创建测试。
- `integration,e2b_integration,docker_integration` 标签下的 sandbox/tools 测试代码编译通过，未执行需要外部服务的集成测试。未修改前端，未运行前端或浏览器检查。
- 本批只删除无调用代码，复用现有包测试和编译检查，没有添加仅断言函数不存在的测试。
- 推送检查暴露新合入的 fork 测试未隔离 Git hook 环境：继承的 `GIT_DIR` 等变量使临时仓库操作指向当前仓库。测试辅助函数现清除继承的 `GIT_*` 变量；真实仓库测试主动注入指向临时路径的仓库变量，验证旧实现失败、修复后通过。该测试修复单独提交，不改变生产逻辑。

## 第六批已实施：外部来源文件名清理

基于第五批合入后的 `10973ed32`，IMA、语雀、RSS 复用 `datasource.SanitizeFileName`，统一无效标点替换、空标题回退和 200 字节 UTF-8 边界截断。计入新增共享实现后，生产 Go 源码净减少 49 行。

- IMA 的媒体与笔记路径、语雀文档直接调用共享函数；RSS 仅保留换行/制表符转空格和首尾空白清理，再调用共享函数。
- 保留原有扩展名拼接、源标题、空白、控制字符和无效 UTF-8 输入处理行为。飞书的扩展名保留、钉钉的控制字符/尾部标点清理、Confluence 的按字符截断规则不同，本批未合并。
- 将两份旧 helper 测试收敛到共享函数测试，补充 16 个边界用例；在实际连接器读取/组装路径新增 15 个用例，覆盖 IMA 两类内容、语雀和 RSS 的最终文件名。
- 用重构前的三个实现对 30,000 组确定性生成输入执行差异校验，结果一致；输入包含中文、emoji、空白、非法字节和截断边界。临时对比代码未留在仓库，避免长期维护旧实现副本。
- 4 个受影响包测试通过：104 项顶层测试，包含既有同步、游标和错误处理测试。未修改前端，未运行浏览器检查或真实外部来源服务测试。

## 第七批已实施：知识库标签筛选与遗留样式

基于第六批合入后的 `820a14daf`，文档页与 FAQ 页复用 `KnowledgeTagFilter`，统一筛选浮层、多选、清空、搜索和管理入口。计入新增组件后，生产前端源码净减少 1,061 行。

- 页面继续负责标签 API、分页、搜索防抖、持久化选择和列表刷新；共享组件只负责展示与交互，保留文档数量/FAQ 分块数量、清空占位文案和管理权限差异。
- 删除两份无效的已选标签补齐逻辑：缺失项原本仍从同一个标签列表生成的映射中查找，因此不可能补齐；未加载的选中 ID 仍保留在选择状态中。
- 删除 `KnowledgeBase.vue` 中 682 行失效 scoped 样式，包括已移入子组件的卡片菜单、标签、悬浮详情和旧上传区域样式。保留仍在当前模板使用的骨架卡片、FAQ 容器和响应式布局。
- 对迁移前后编译后的 CSS 做临时声明对比，检查 2,196 项声明，确认保留的父组件规则与两种筛选界面的原有属性值一致；这不替代浏览器视觉验收。
- 新增 9 项真实 Vue 组件行为测试，覆盖两种计数、多选/清空、搜索、加载/空态、分页、管理权限及父级选择更新。
- `scripts/verify_frontend_pr.sh` 通过：957 项测试通过，1 项原有测试跳过，类型检查与生产构建通过。未做浏览器视觉验收；未修改后端。

## 后续优先级

| 优先级 | 证据 | 建议边界 | 验证重点 |
| --- | --- | --- | --- |
| P2 | `AgentEditorModal.vue` 6,756 行，其中 script 3,023 行；近期变更 9 次 | 按提示词编辑、知识库选择、工具配置分离状态和表单序列化责任 | 编辑回填、模式切换、保存 payload 保持兼容 |
| P2 | `WikiBrowser.vue` 6,592 行，其中 script 4,086 行；`FAQEntryManager.vue` 5,650 行，其中 style 2,780 行 | 分开文档导航/编辑状态、批量动作与可复用展示区域 | 页面切换后的异步结果、选中项、保存与批量操作 |
| P2 | `knowledge_process.go` 4,091 行；`processChunks` 470 行、`ProcessDocument` 441 行 | 先划分分块入库、摘要、问题生成、重解析阶段的输入与输出，再抽函数 | 任务归属、取消、失败回写、重试与索引状态 |
| P2 | `wiki_ingest_batch.go` 的 `ProcessWikiIngest` 718 行、`mapOneDocument` 446 行、`reduceSlugUpdates` 422 行 | 分离任务协调、单文档处理、页面归并；保留现有检查点 | 删除 KB、重复投递、部分失败、并发归并 |
| P2 | `internal/im/service.go` 3,493 行，`handleMessageStream` 450 行 | 按通道生命周期、请求准备、流式回复拆责任 | completion/error 顺序、取消、最终持久化与平台发送失败 |

这些条目的行号、长度和变更频率是本次基线快照。仅按长度分文件不会自动降低复杂度，验收应检查状态归属和业务规则是否集中。

### 后端重复的具体判断

- Qdrant 与 Weaviate 的 `tokenizeQuery` 函数体相同，但只有 Qdrant 的关键字检索调用它；Weaviate 使用 `WithQuery(params.Query)`。第五批已删除 Weaviate 的失效函数，保留 Qdrant 实现。
- 第六批已合并 IMA、语雀、RSS 的文件名标点替换和 UTF-8 截断逻辑；RSS 的空白预处理保留在连接器内。飞书、钉钉、Confluence 的其他规则继续独立维护。
- Milvus/Qdrant/Weaviate 的结果映射体相似，但接收不同后端类型。当前各约 17 行，为此引入通用接口或泛型转换层未必减少维护成本。
- 终端和桌面 WebSocket ticket handler 重复检查会话、用户、租户和 token，但签发方式不同。后续可共享前置身份校验，保留 ticket 和连接生命周期各自的契约。
- 资源权限已经有 `internal/application/access`；继续沿已有授权边界收敛，避免新建与其并行的权限工具层。

### 第五批已核实并删除的 Go 私有自由函数

初次扫描的 16 个候选和 2 个仅被候选调用的辅助函数已清理：

| 文件（相对 `internal`） | 函数 |
| --- | --- |
| `application/repository/retriever/weaviate/repository.go` | `tokenizeQuery` |
| `im/wechat/crypto.go` | `encryptAES128ECB` |
| `application/repository/retriever/milvus/filter.go` | `formatValue`、`escapeDoubleQuotes` |
| `agent/tools/todo_write.go` | `getStringArrayField`、`getStringField` |
| `agent/prompts.go` | `formatFileSize` |
| `utils/inject.go` | `extractTableAliasMap`、`collectTableAliases` |
| `application/service/wiki_ingest_dedup.go` | `countEntityConceptPages` |
| `agent/tools/sandbox_ls.go` | `relativeSkillFileFromImagePath` |
| `infrastructure/docparser/json_converter.go` | `indentJSON` |
| `handler/knowledge.go` | `mimeTypeByExt` |
| `application/service/tenant_skill_source.go` | `normalizeFetchedSkillArchive` |
| `sandbox/gateway_transport.go` | `newGatewayDataTransport` |
| `application/service/knowledge_process.go` | `buildSplitterConfig` |
| `im/service.go` | `imLocalStorageBaseDir` |
| `handler/session/helpers.go` | `getRequestID` |

## 测试维护与验证结果

抽查发现，标签弹窗、安装进度的部分测试通过正则检查源码文本；IM 部分流式测试使用镜像状态结构，同时也存在直接调用生产路径的退出/投递测试。重构时应逐步补真实行为覆盖，不应只调整正则或同时修改两份镜像逻辑来获得通过。

第一批验证：

- 修改前 `npm run type-check` 通过，作为类型检查基线。
- 11 项定向测试通过，其中 7 项是新增行为测试。
- `scripts/verify_frontend_pr.sh` 完整通过：907 项测试、`vue-tsc --build`、Vite 生产构建。
- 构建仍报告大 chunk 提示；本轮未以 bundle 体积为目标，也没有测量构建前后的体积差异。
- 未修改后端，未运行后端测试；未做浏览器视觉验收。

下一批优先检查终端与桌面 WebSocket ticket 的重复身份校验，沿现有授权边界确定共享范围。涉及聊天、任务重试、权限或共享状态的改动应单独成批，以可验证的行为等价为边界。
