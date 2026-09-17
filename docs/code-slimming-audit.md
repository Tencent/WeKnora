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

## 后续优先级

| 优先级 | 证据 | 建议边界 | 验证重点 |
| --- | --- | --- | --- |
| P1 | `useChatCitationPopover.ts` 与 `useEmbedCitationPopover.ts` 重复浮层状态、定位、计时和事件绑定 | 共享交互状态；通过参数提供内容加载、缓存作用域和点击行为 | 普通/嵌入鉴权、缓存隔离、根节点替换、悬停竞争与卸载 |
| P2 | `AgentEditorModal.vue` 6,756 行，其中 script 3,023 行；近期变更 9 次 | 按提示词编辑、知识库选择、工具配置分离状态和表单序列化责任 | 编辑回填、模式切换、保存 payload 保持兼容 |
| P2 | `WikiBrowser.vue` 6,592 行，其中 script 4,086 行；`FAQEntryManager.vue` 5,650 行，其中 style 2,780 行 | 分开文档导航/编辑状态、批量动作与可复用展示区域 | 页面切换后的异步结果、选中项、保存与批量操作 |
| P2 | `knowledge_process.go` 4,091 行；`processChunks` 470 行、`ProcessDocument` 441 行 | 先划分分块入库、摘要、问题生成、重解析阶段的输入与输出，再抽函数 | 任务归属、取消、失败回写、重试与索引状态 |
| P2 | `wiki_ingest_batch.go` 的 `ProcessWikiIngest` 718 行、`mapOneDocument` 446 行、`reduceSlugUpdates` 422 行 | 分离任务协调、单文档处理、页面归并；保留现有检查点 | 删除 KB、重复投递、部分失败、并发归并 |
| P2 | `internal/im/service.go` 3,493 行，`handleMessageStream` 450 行 | 按通道生命周期、请求准备、流式回复拆责任 | completion/error 顺序、取消、最终持久化与平台发送失败 |

这些条目的行号、长度和变更频率是本次基线快照。仅按长度分文件不会自动降低复杂度，验收应检查状态归属和业务规则是否集中。

### 后端重复的具体判断

- Qdrant 与 Weaviate 的 `tokenizeQuery` 函数体相同，但只有 Qdrant 的关键字检索调用它；Weaviate 使用 `WithQuery(params.Query)`。后续应验证并删除 Weaviate 的失效函数，而非为了它增加共享分词层。
- IMA、语雀的 `sanitizeFileName` 函数体相同，可以共享；RSS 的近似实现另外处理首尾空白和换行，合并时必须保留这些差异。
- Milvus/Qdrant/Weaviate 的结果映射体相似，但接收不同后端类型。当前各约 17 行，为此引入通用接口或泛型转换层未必减少维护成本。
- 终端和桌面 WebSocket ticket handler 重复检查会话、用户、租户和 token，但签发方式不同。后续可共享前置身份校验，保留 ticket 和连接生命周期各自的契约。
- 资源权限已经有 `internal/application/access`；继续沿已有授权边界收敛，避免新建与其并行的权限工具层。

### 未引用 Go 私有自由函数候选

扫描发现 16 个，第一批暂未改动后端。后续逐包确认调用、构建入口和测试后处理：

| 文件（相对 `internal`） | 函数 |
| --- | --- |
| `application/repository/retriever/weaviate/repository.go` | `tokenizeQuery` |
| `im/wechat/crypto.go` | `encryptAES128ECB` |
| `application/repository/retriever/milvus/filter.go` | `formatValue` |
| `agent/tools/todo_write.go` | `getStringArrayField`、`getStringField` |
| `agent/prompts.go` | `formatFileSize` |
| `utils/inject.go` | `extractTableAliasMap` |
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

下一批优先处理主聊天与嵌入聊天引用浮层的重复。涉及聊天、任务重试、权限或共享状态的改动应单独成批，以可验证的行为等价为边界。
