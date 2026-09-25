# Wiki 能力

Wiki 将知识库资料整理为相互链接的 Markdown 页面。文档入库后，模型提取人物、产品和概念等条目，生成带来源引用的页面，便于按主题浏览。

用户可直接编辑生成的页面，查看历史版本并回滚。智能体也可通过 Wiki 工具读取和维护页面；访问范围仍由知识库权限决定。

<Screenshot
  src="/screenshots/wiki-browser.png"
  caption="Wiki 浏览器：左侧目录树，右侧生成的条目页面与出处"
  hint="展示左侧按类型分组的目录树、一篇实体页正文、页内 wiki 链接与来源文档引用。" />

## 开启并浏览 Wiki {#怎么开启}

1. 编辑知识库 → 「索引策略」里打开 **Wiki**；
2. 上传文档（已有文档也会被纳入，无需重传）；
3. 等待生成。Wiki 生成是异步的，文档多时会持续一段时间，知识库面包屑上有「索引中」提示；
4. 完成后进入知识库的 **Wiki** 页签浏览，**图谱**页签可以看条目之间的链接关系。

Wiki 生成会增加模型调用，用量取决于文档规模和抽取密度。可在知识库 Wiki 配置中选择 `focused`、`standard` 或 `exhaustive`，详细说明见[抽取粒度](#抽取粒度)。

<Screenshot
  src="/screenshots/wiki-graph.png"
  caption="Wiki 图谱视图：条目之间的链接关系"
  hint="展示图谱概览模式，节点按页面类型着色，可点击跳转到具体页面。" />

## 编辑和回滚页面

在 Wiki 页面中编辑正文后，可查看版本历史并比较改动。历史记录区分自动生成、智能体、人工编辑和回滚来源。回滚会以所选历史内容创建新版本，版本号继续递增。

系统优先清理自动生成的旧版本：达到 50 个历史版本后清理可裁剪快照，达到 200 个时按硬上限清理全部来源。需要长期保存的内容应另行归档。升级前写入的版本或已被清理的版本没有快照：选中这类版本时历史面板显示提示而不是差异；对比基线缺失时直接展示所选版本的全文。其他版本的比较和回滚不受影响。

## 查看变更与生成状态

页面创建、更新、删除和批量生成的操作记录统一显示在「知识库 → 设置 → 活动」。文档处理期间可查看索引状态；服务重启后，系统会恢复持久化的待处理 Wiki 任务。删除知识库时，其 Wiki 页面、目录、问题和版本历史一并清理。接口兼容与恢复机制见参考部分。

## 版本与运行参考

### 人工编辑与版本历史

Wiki 页面支持人工编辑和版本回滚，用户可修正生成内容，并查看各版本的来源、操作者和时间。版本记录由迁移 `000075` 引入。

#### 版本来源与操作者 {#每个版本记的是谁改的}

`wiki_pages` 上有两个溯源字段，`last_edit_source` 标记**当前版本**的作者类型：

| `edit_source` | 含义 |
| --- | --- |
| `pipeline` | Wiki 生成管道写的（历史遗留行为空串，按 `pipeline` 处理） |
| `agent` | Agent 通过 `wiki_write_page` / `wiki_replace_text` 等工具写的 |
| `user` | 人工在编辑器里改的 |
| `revert` | 回滚产生的版本 |

配套的 `last_editor_id` 记录操作者（后台管道写入时为空）。界面据此展示每个版本的编辑来源。

#### 快照与回滚

- 每次页面被覆盖前，**旧版本**先整份快照进 `wiki_page_revisions`（标题、正文、摘要、页面类型、状态、别名，加上该版本的作者与时间）；当前版本只存在于 `wiki_pages` 里。`(page_id, version)` 上的唯一索引配合 `ON CONFLICT DO NOTHING`，让「先快照再更新」这条写路径在重试下保持幂等；
- `GET /revisions/*slug` 列历史版本（倒序，不含正文，附当前版本号）；带 `?version=N` 则取该版本全文，用于 diff；
- `POST /revert` 传 `{slug, version}` 回滚。回滚会使用目标版本的内容创建新版本，版本号继续递增，`edit_source` 记为 `revert`，因此回滚本身也可被回滚。回滚到当前版本会返回 400（通常意味着前端拿的是过期的历史列表）。

#### 历史保留策略

版本历史按两级上限清理（`internal/types/wiki_page.go`）：

- **软上限 50 版**：只清理「可裁剪」的快照，即 `pipeline` 写的和历史遗留的空来源；
- **硬上限 200 版**：不分作者一律裁剪，保证纯人工维护的页面存储也有界。

在达到硬上限前，系统优先清理自动生成的历史，保留人工编辑记录。

<Screenshot
  src="/screenshots/wiki-revision-history.png"
  caption="Wiki 页面版本历史：按来源区分的版本列表与回滚入口"
  hint="展示某个 wiki 页面的历史抽屉，含版本号、编辑来源（管道/人工/Agent/回滚）、编辑者与时间，以及对比/回滚按钮。" />

### 操作历史（知识库活动流）

Wiki 曾经维护一份独立的操作日志（`wiki_log_entries` 表 + `GET /wiki/log` 接口 + WikiBrowser 里的日志页签）。这份 feed 与知识库活动流内容重叠，已在 migration `000077_remove_wiki_log` 中整体移除：表被 DROP，历史遗留的 `page_type = 'log'` 页面一并删除，`log` 不再是合法页面类型。

现在**知识库活动流是唯一的操作历史入口**：

- ingest 批次结束时，`wiki_ingest_batch.go` 汇总本批各类动作数量，调用 `service.RecordWikiContentActivity()` 写一条 `wiki_content_changed` 活动；
- 人工在 WikiBrowser 中创建/更新/删除页面时，`internal/handler/wiki_page.go` 同样把 `manual_create` / `manual_update` / `manual_delete` 投影到活动流；
- 活动记录落在审计日志体系（`kb_activity.go` → `AuditLogService`），可在「知识库 → 设置 → 活动」查看，保留策略与其它审计日志一致（见[可观测性与审计](16-observability.md)）；
- 写入是 best-effort：活动记录失败不会让 wiki 编辑本身失败。

升级注意：如果外部集成还在调用 `GET /api/v1/knowledgebase/:kb_id/wiki/log`，需要改用知识库活动流接口 `GET /api/v1/knowledge-bases/:id/activity`。

### 失败恢复

`internal/container/recover_pending_wiki_tasks.go` 在服务启动时闭合 Lite 模式（进程内 `SyncTaskExecutor`）或 Redis 入队中断留下的缺口：

1. 扫描持久化的 `task_pending_ops` 表中 `scope = knowledge_base` 且 `task_type ∈ {wiki:ingest, wiki:finalize}` 的待处理组合；
2. 清理已删除 KB 或所属租户已被删除的残留行（fail-closed）；
3. 对每个活跃 KB 重新入队触发任务：`wiki:ingest` 不带 TaskID（允许多批并发），`wiki:finalize` 使用 `"wiki-finalize-" + KB_ID` 去重（同一 KB 只保留一个 finalize）。重复入队无害——ingest 认领互不相交的行，finalize 在 lane 内合并。

运行期间的其他保护：

- **租户已删除**：ingest / finalize 任务在调用模型前检查租户是否存活，租户已删除则丢弃该知识库的队列，不再产生模型请求；
- **Wiki 无法运行**：触发时发现知识库已关闭 Wiki、没有合成模型或模型已被删除，会清空尚未认领的 ingest 操作并释放对应文档，文档不会一直停在「索引中」；
- **触发任务丢失**：只有持久化操作、没有触发任务的知识库由后台巡检重新触发，每个知识库每个巡检阈值周期最多一次；
- **瞬时错误重试**：模型调用遇到 408/429/5xx、超时、连接重置，或带有限流字样（如「调用频率超限」、`rate limit`）的 403 时按指数退避重试；鉴权类 403 不重试；
- **失败次数优先级**：认领待处理文档时优先处理失败次数少的文档，反复失败的文档不会阻塞新文档。

## 页面与接口参考

### 页面模型与层级

#### 页面类型（PageType）

`internal/types/wiki_page.go` 定义了 6 种页面类型：

| 类型 | 说明 |
| --- | --- |
| `summary` | 单篇源文档的摘要页（slug 形如 `summary/<knowledge-uuid>`） |
| `entity` | 实体页（人、组织、产品、技术等） |
| `concept` | 概念/主题页 |
| `index` | wiki 级索引页（元数据） |
| `synthesis` | 综合分析页，**仅由 Agent 通过 `wiki_write_page` 工具创建** |
| `comparison` | 对比页，**仅由 Agent 通过 `wiki_write_page` 工具创建** |

页面状态（`WikiPageStatus`）：`draft` / `published`（默认）/ `archived`。

#### 目录树（Folder Hierarchy）

migration `000061_wiki_page_hierarchy.up.sql` 引入独立的 `wiki_folders` 表（邻接表模型）：

- `WikiFolder` 以 `ParentID`（空串代表根）+ 物化 `Path`（`/` 连接的名称链）组织树；空文件夹可独立存在，用户可以先搭好骨架；
- `WikiPage.FolderID` 是页面归属的**唯一事实来源**（FK → `wiki_folders.id`，空串表示 wiki 根）；
- 页面上的 `CategoryPath` / `WikiPath` / `Depth` / `SortOrder` 是从 folder 链派生的**缓存投影**；
- 模型生成的分类路径最深 3 级（常量 `WikiCategoryMaxDepth = 3`），`CleanWikiCategoryPath()` 会规范化全角分隔符（`／`、`｜` → `/`）并剔除「实体」「概念」等类型标签；
- 页面放入用户建立的文件夹时，文件夹路径原样作为分类路径，名为「概念」「Concepts」等的文件夹不会被当作类型标签剔除。

#### 关键字段

- `Slug`：页面在 KB 内的唯一标识（见下节）；
- `SourceRefs`：来源引用，格式 `"<knowledge_id>|<doc_title>"`；`ChunkRefs`：分块级证据引用；
- `InLinks` / `OutLinks`：wiki-link 反向/正向链接，维护图结构，`GET /graph` 可查询全局或 ego 视图；
- `Aliases`：别名（用于搜索与去重合并后的旧名指向）；`Version`：版本号。

### 发布与访问

所有 Wiki 路由挂在 `/api/v1/knowledgebase/:kb_id/wiki` 之下（`internal/router/routes_knowledge.go`），**没有免登录的公开访问模式**，读写均受 RBAC 与 KB 访问控制约束：

#### 读接口（Viewer + KBAccessRead）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/pages` | 页面列表 |
| GET | `/pages/*slug` | 按 slug 取单页 |
| GET | `/folders` | 目录树 |
| GET | `/index` | 索引页 |
| GET | `/graph` | 链接图（全局概览 / ego 模式） |
| GET | `/stats` | 统计 |
| GET | `/search?q=...` | 搜索 |
| GET | `/lint` / `/issues` | 质量检查结果 / 问题列表 |
| GET | `/revisions/*slug` | 版本历史列表；带 `?version=N` 取该版本全文 |

`KBAccessRead` 覆盖：KB 所有者、组织共享、以及通过共享 Agent 获得的访问。

#### 写接口（OwnedWikiKBOrAdmin + KBAccessWrite）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST / PUT / DELETE | `/pages`、`/pages/*slug` | 创建 / 更新 / 删除页面 |
| POST / PUT / DELETE | `/folders`、`/folders/:folder_id` | 目录管理 |
| PUT | `/move-page` | 移动页面到目录 |
| POST | `/rebuild-links` | 重建链接图 |
| POST | `/auto-fix` | 触发自动修复 |
| PUT | `/issues/:issue_id/status` | 更新问题状态 |
| POST | `/revert` | 回滚到指定版本（body：`{slug, version}`） |

写权限按 KB 归属判定：贡献者只要拥有该 KB 即可管理其 wiki，否则 403。API Key 场景按 `ingest` / `retrieve` capability 映射。

前端由 `WikiBrowser.vue` 提供浏览界面；文档解析期间 `wikiStatusRefresh.ts` 轮询 `parse_status`（`pending` / `processing` / `finalizing`），解析完成而摘要仍在生成时继续轮询。目录树的展开状态由 `wikiDirectoryState.ts` 单独维护：新建文件夹或刷新数据后，`expandWikiDirectoryPath()` 会把当前路径上的各级目录标记为「用户已展开」，避免刷新时整棵树塌回默认折叠状态。

### 与 Agent 的关系

智能体可通过以下 9 个 Wiki 工具读取、修改页面并处理问题（回读源文档则复用通用的 `read_document`），工具定义位于 `internal/agent/tools/definitions.go`：

| 工具 | 作用 | 关键参数 |
| --- | --- | --- |
| `wiki_read_page` | 按 slug 批量读取页面全文 | `slugs: string[]` |
| `wiki_search` | 搜索页面（标题 / slug / 别名 / 摘要 / 内容；`query` 按大小写不敏感的 POSIX 正则解释，不是合法正则的文本如 `C++` 按字面匹配；`regex=false` 强制字面匹配） | `query`、`regex?`、`knowledge_base_ids?`、`limit?`（旧参数 `queries`、`knowledge_base_id` 仍接受） |
| `wiki_write_page` | 创建/整页覆盖（`synthesis`、`comparison` 页只能由此创建） | `slug`、`title`、`summary`、`content`、`page_type`、`aliases?`、`source_refs?` |
| `wiki_replace_text` | 页内精确文本替换 | `slug`、`old_text`、`new_text` |
| `wiki_rename_page` | 重命名 slug，自动更新反向链接 | `slug`、`new_slug` |
| `wiki_delete_page` | 删除页面并清理死链 | `slug` |
| `read_document` | 回读源文档原文：元数据头 + 分块，可分页或用 `query` 在文档内定位（替代原 `wiki_read_source_doc`；仅 Wiki 的知识库同样可用，因为分块总会落库） | `id`（`dN` / `cN`）、`offset?`、`limit?`、`query?`、`regex?`、`context?` |
| `wiki_flag_issue` | 标记页面问题 | `slug`、`issue_type ∈ {mixed_entities, contradictory_facts, out_of_date, other}`、`description` |
| `wiki_read_issue` | 查看问题详情 | 问题 ID |
| `wiki_update_issue` | 更新问题状态 | 问题 ID、`status ∈ {pending, ignored, resolved}` |

推荐的阅读顺序是 `wiki_search` → `wiki_read_page` → `read_document`：先定位页面，再读整页，需要精确引用时回到原始来源的 `cN` 分块。工具输出为 XML-like 结构（`<wiki_page><metadata>...<summary>...<content>...`），前端用 `frontend/src/utils/wikiToolReferences.ts` 的 `parseWikiToolReferences()` 解析成引用卡片渲染在对话中。

配套机制：

- **Wiki Scope**：Agent 会话内维护 wiki KB 白名单，支持通过 `@mention` 把范围收窄到特定文档/标签，工具执行时自动过滤 `source_refs`（`internal/agent/tools/wiki_tools.go`）；
- **写权限**：检索范围只需要读权限，而改写类工具（`wiki_write_page`、`wiki_replace_text`、`wiki_rename_page`、`wiki_delete_page`、`wiki_flag_issue`、`wiki_update_issue`）只作用于调用方可编辑的知识库，即本空间的知识库，或通过组织分享获得 editor 及以上权限的知识库。调用方本身还要有写权限：空间角色 Contributor 及以上，受限 API key 需要 `ingest` 能力。以 Viewer 身份运行的 IM、网页嵌入和 MCP 端点因此只读。范围内没有可编辑的 wiki 知识库时，这些工具不会注册。共享智能体始终只读；
- **Wiki Fixer**：内置 Agent（`types.BuiltinWikiFixerID`），负责自动修复 wiki 问题（死链、实体混淆等）。跨租户访问共享 KB 时要求租户角色 ≥ Editor，并自动提升到源租户上下文（`internal/handler/session/wiki_fixer_scope.go`）。提升后使用内置默认配置，只针对这一个 KB，不启用 MCP、技能、沙箱和联网搜索，模型回退到该 KB 自己的模型，调用方对 fixer 的自定义配置不会带进源空间；
- **问题闭环**：`wiki_page_issues` 表 + lint 接口 + `auto-fix`，人和 Agent 都可以报告/处理问题。

### 生成流程

Wiki 生成由**文档摄入（knowledge ingest）触发**，经 Redis 任务队列异步执行。任务类型定义在 `internal/types/task.go`：

```go
TypeWikiIngest   = "wiki:ingest"
TypeWikiFinalize = "wiki:finalize"
```

整个管道分四个阶段（Map-Reduce 结构）：

| 阶段 | 任务 | 做什么 | LLM 提示词（`internal/agent/prompts_wiki.go`） |
| --- | --- | --- | --- |
| Pass 0：候选抽取 | `wiki:ingest` | 从文档抽取候选 slug 骨架（entities + concepts 的 JSON） | `WikiCandidateSlugPrompt` |
| Pass 1..N：分块引文 | `wiki:ingest` | 逐 chunk 为候选 slug 标注引用，输出 `{ citations: {"slug": ["c001", ...]}, new_slugs: [...] }`；长前缀复用 prefix caching | `WikiChunkCitationPrompt` |
| Reduce：页面合并 | `wiki:ingest` | 按 slug 增量更新或合并页面，输出 `SUMMARY: ...` + Markdown 正文；严格接地、禁止幻觉、去重、禁止自链接 | `WikiPageModifySystemPrompt` + `WikiPageModifyUserPrompt` |
| Finalize：收尾 | `wiki:finalize` | 重建索引页、清理死链、补交叉链接、目录修剪——纯 SQL/图算法，**不调用 LLM** | — |

辅助提示词：

- `WikiTaxonomyPlanPrompt`：为同一批次的所有实体/概念统一规划目录路径（最多 2 级、优先复用已有文件夹），保证目录树连贯；
- `WikiDeduplicationPrompt`：判断新抽取项是否与既有页面同指一物，核心原则是 **"related ≠ same"**（相关不等于相同），返回 `{ merges: { "entity/new": "entity/existing" } }`。名称与别名双向比对（名称对别名、别名对别名），但共享的别名或缩写本身不足以判定为同一事物。

去重与写入的其他规则：

- **同名跨类型复用**：重新解析时模型可能把同一事物先判为 `concept/X`、后判为 `entity/X`。没有同类型页面时，标题归一化后相同的另一类型页面会被直接更新，而不是再建一个孪生页面；
- **引用图片带说明**：Reduce 阶段读取被引用分块时，会把图片的 caption / OCR 文本内联进分块内容，模型能判断图片是否与页面相关；
- **整页重写被截断**：整页重写因输出长度上限被截断时，最多续写 3 轮并无缝拼接；仍未写完则放弃本次写入并记录警告，不会把半页内容写回页面；
- **单个汉字不自动加链接**：标题只有一个汉字的页面仍可存在，但不会在其他页面正文中自动为该字添加链接，避免把「风沙」「核心」等词误链接。

#### 抽取粒度

`WikiConfig`（存于 `knowledge_bases.wiki_config` JSONB 列）中的 `WikiExtractionGranularity` 控制抽取密度：

| 粒度 | 行为 |
| --- | --- |
| `focused` | 仅 3-7 个主要主题 |
| `standard`（默认） | 主题 + 被实质性讨论（一段/多条/2-3 句以上）的实体概念 |
| `exhaustive` | 穷举所有命名事物与公认概念 |

#### 并发与批处理

`WikiConfig` 相关参数（`internal/types/wiki_page.go`）：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `IngestBatchSize` | 5 | 单批认领的待处理文档数 |
| `IngestMapParallel` | 10 | Map 阶段（每文档抽取+引文）errgroup 并发数 |
| `IngestReduceParallel` | 10 | Reduce 阶段（每 slug 写页面）并发数 |
| `IngestMaxInflight` | 4 | 同一 KB 最大并发批次（保证跨 KB 公平） |

目录规划阶段计算文件夹与条目的 embedding 时，和其他 embedding 调用一样按 `BATCH_EMBED_SIZE` 分批请求，不会因单次输入过多被模型服务拒绝。

#### 生成流程图

```mermaid
flowchart TD
    A["文档入库 (knowledge ingest)"] --> B["任务入队 wiki:ingest (Redis 队列)"]
    B --> C["Pass 0: 候选 slug 抽取<br/>WikiCandidateSlugPrompt"]
    C --> D["Taxonomy 规划<br/>WikiTaxonomyPlanPrompt 统一目录路径"]
    C --> E["Pass 1..N: 分块引文标注<br/>WikiChunkCitationPrompt + prefix caching"]
    E --> F["去重判定<br/>WikiDeduplicationPrompt (related ≠ same)"]
    F --> G["Reduce: 按 slug 并发写页面<br/>WikiPageModifySystemPrompt<br/>增量合并 / 新建, 强制引用接地"]
    D --> G
    G --> H["写入 wiki_pages<br/>变更投影到知识库活动流 (audit)"]
    H --> I["任务入队 wiki:finalize<br/>(TaskID = wiki-finalize-KBID, 同 KB 去重)"]
    I --> J["Finalize: 重建索引 / 清理死链 / 交叉链接<br/>纯 SQL 与图算法, 无 LLM"]
    J --> K["published 页面在 WikiBrowser 可浏览<br/>Agent 工具可读写"]
```

### Slug 机制

- **格式**：`<type>/<name>`，如 `entity/acme-corp`、`concept/rag`、`summary/<knowledge-uuid>`；小写、连字符分隔，非拉丁文名做罗马化/拼音；
- **唯一性**：数据库唯一索引（`000037_wiki_and_indexing.up.sql`）：

  ```sql
  CREATE UNIQUE INDEX idx_kb_slug ON wiki_pages (knowledge_base_id, slug) WHERE deleted_at IS NULL
  ```

  即 slug 在**单个知识库内唯一**，跨 KB 可以重复；
- **稳定性**：文档更新重新抽取时，提示词强制模型复用旧 slug——

  > If an entity or concept from the previous extraction still exists in the current document, **reuse its exact slug** from the previous list. Do NOT generate a new slug for the same thing.

  只有新出现的事物才生成新 slug，消失的项不再输出；
- **Slug Handle（句柄代理）**：ingest 的 LLM 调用中，高熵的真实 slug（尤其含 UUID 的 `summary/...`）会被替换为短句柄（`ref-1`、`ref-2`），模型输出 `[[ref-1|title]]` 后由后端还原为真实 slug，避免模型抄错 UUID（`internal/application/service/wiki_slug_handles.go`）；
- **引用作用**：Agent 回答中的 wiki 引用以 `[[slug|title]]` 形式出现，`InLinks`/`OutLinks` 依 slug 维护页面图；重命名 slug（`wiki_rename_page` 工具）会自动更新所有反向链接。

## 实现参考

以下路径均相对仓库根目录：

| 层 | 文件 |
| --- | --- |
| 数据结构 | `internal/types/wiki_page.go` |
| HTTP Handler | `internal/handler/wiki_page.go` |
| 生成管道 | `internal/application/service/wiki_ingest.go`、`wiki_ingest_batch.go`、`wiki_ingest_cite.go`、`wiki_ingest_dedup.go`、`wiki_ingest_taxonomy.go` |
| 页面服务 | `internal/application/service/wiki_page.go`、`wiki_linkify.go`、`wiki_lint.go`、`wiki_slug_handles.go` |
| LLM 提示词 | `internal/agent/prompts_wiki.go` |
| Agent 工具 | `internal/agent/tools/wiki_*.go`（注册于 `internal/agent/tools/definitions.go`） |
| 失败恢复 | `internal/container/recover_pending_wiki_tasks.go` |
| 路由 | `internal/router/routes_knowledge.go` 的 `RegisterWikiPageRoutes`（行为测试见 `internal/router/router_wiki_test.go`） |
| 数据库迁移 | `migrations/versioned/000037_wiki_and_indexing.up.sql`、`000061_wiki_page_hierarchy.up.sql`、`000077_remove_wiki_log.up.sql` |
| 前端 | `frontend/src/views/knowledge/wiki/WikiBrowser.vue`、`frontend/src/api/wiki/`、`frontend/src/utils/wikiToolReferences.ts` |
