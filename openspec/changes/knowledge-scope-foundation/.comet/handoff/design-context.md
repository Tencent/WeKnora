# Comet Design Handoff

- Change: knowledge-scope-foundation
- Phase: design
- Mode: compact
- Context hash: ce1053c2f66ff6e7115e0c9acb9f2fdd60d3870dabd69f668a6c97fe5bfa4a8a

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## openspec/changes/knowledge-scope-foundation/proposal.md

- Source: openspec/changes/knowledge-scope-foundation/proposal.md
- Lines: 1-40
- SHA256: 745f454308af65cd3a98028204203bd982a058260bf86605566ff1c1e28095b6

```md
## Why

WeKnora 现有“多知识库 + 标签”能力可以支持知识分类，但无法确定性保证业务系统及其发布版本之间的数据隔离：标签是辅助分类且存在 OR 语义，RAG、Agent 工具和 Neo4j 也没有共享同一个版本范围。当前需要先建立统一、可审计、后端强制的知识范围底座，确保后续安全风险关联分析和模板报告不会发生跨系统串数。

## What Changes

- 新增部门公共、系统公共、系统版本专属三类结构化知识范围。
- 规定一个业务系统对应一个 WeKnora 知识库，并维护稳定 `system_id`、多个业务版本和唯一当前版本。
- 一个租户配置一个默认部门公共知识库，系统问答时由后端强制加入。
- 上传文件、URL 和手工知识时原子写入文档及其结构化范围；异步任务按 `knowledge_id` 重新读取可信范围。
- 查询时按每个系统独立解析指定或当前版本，并将 `project_id`、标签和明确文档选择限制为“只能缩小、不能扩大”。
- RAG、Agent 知识搜索、Agent 图谱查询和 Neo4j 实体检索统一消费同一个后端 `ResolvedQueryScope`。
- 检索结果增加系统、版本、项目和原始文档来源；部分系统失败时明确标记结果不完整。
- 新增显式的存量文档范围预览/确认迁移流程，不根据标签自动提交范围。
- 新增 `ENABLE_KNOWLEDGE_SCOPE` 功能开关；未登记知识库和关闭开关时保留现有行为。
- 增加知识库范围管理、版本管理、上传范围选择和问答版本选择界面。
- 本 change 保持为一个整体，因为范围隔离是跨上传、检索、Agent 与图谱的单一安全不变量；功能开关保证整体验证完成前不会部分启用。

本 change 不建设统一风险事实模型、风险分析工具、Word 模板渲染器、跨系统自动图关系、项目级权限或已处理文档的跨版本移动。

## Capabilities

### New Capabilities

- `knowledge-scope-catalog`: 管理租户级部门公共/系统知识容器、业务系统版本、当前版本和显式存量范围迁移。
- `scoped-knowledge-ingestion`: 在知识创建和异步处理链路中校验、原子保存并继承可信的系统/版本/项目范围。
- `scoped-knowledge-retrieval`: 将系统版本范围统一应用到 RAG、直接检索、Agent 工具和 Neo4j，并输出可追溯来源与部分失败告警。

### Modified Capabilities

无。当前 OpenSpec 尚无已归档 capability；本次以新增 capability 方式建立基线。

## Impact

- 数据库：新增 PostgreSQL 与 SQLite 的范围容器、系统版本、文档范围表及唯一性/引用约束。
- 后端：新增 Scope Catalog/Resolver 深模块，扩展知识上传、会话检索、Agent 工具、Neo4j Repository、路由和依赖注入。
- API：上传请求增加可选 `knowledge_scope`，问答/检索请求增加可选 `system_scopes`，并新增范围与版本管理 API。
- 前端：扩展知识库设置、上传确认、存量迁移、多知识库选择和引用展示。
- 运维：新增 `ENABLE_KNOWLEDGE_SCOPE`；关闭时不改变旧知识库行为。
- 兼容性：请求字段均为增量字段；已登记系统知识库启用功能后执行 fail-closed 校验，未登记知识库继续走旧路径。

```

## openspec/changes/knowledge-scope-foundation/design.md

- Source: openspec/changes/knowledge-scope-foundation/design.md
- Lines: 1-92
- SHA256: 87828f70e94c052561e57d0592139b9ba7cb226fec305253c833b54b3169a73f

[TRUNCATED]

```md
## Context

WeKnora 当前以知识库和文档标签组织知识。知识库可承担系统级容器，但标签主要用于分类，且文档多标签查询存在 OR 语义；聊天检索、Agent 工具和 Neo4j 也各自处理知识库或文档参数，无法共同证明某次查询只访问了指定系统版本。

本变更服务于同一租户内的业务系统数据隔离。所有用户仍可访问全部系统，但每次查询必须由用户选择的系统/版本限定。一个系统的一个业务版本可以涉及多个项目，`project_id` 只作为可选筛选和来源字段。详细设计基线见 `docs/superpowers/specs/2026-07-14-system-version-knowledge-scope-design.md`。

## Goals / Non-Goals

**Goals:**

- 用数据库结构而非标签/提示词表达部门、系统和业务版本范围。
- 以一个后端 Scope Resolver 生成写入和查询的可信范围。
- 让 RAG、直接检索、Agent 工具和 Neo4j 使用相同的文档边界。
- 自动组合默认部门公共、系统公共和选定版本知识。
- 保留存量未登记知识库的现有行为，并支持显式迁移。
- 保持新增模块和请求字段可选、集中，降低合并 Tencent 上游的冲突面积。

**Non-Goals:**

- 不实施用户/角色到系统的访问控制。
- 不建设项目主数据或项目级强隔离。
- 不实现统一风险事实库、风险分析工具或 Word 模板报告。
- 不自动创建跨系统 Neo4j 关系。
- 不支持已处理文档直接跨版本移动。
- 不把业务版本状态解释为冻结或禁止继续上传。

## Decisions

### 1. 一个系统知识库对应一个稳定系统标识

`scope_containers` 将知识库声明为部门公共或系统容器。系统容器保存租户内唯一的稳定 `system_id`；系统名称和知识库名称可以修改，但不能参与隔离判断。

选择该方案而不是“所有系统共享一个知识库”，因为现有 WeKnora 的权限、检索和图谱命名空间天然以知识库为主要边界。选择结构化映射而不是从名称推导，避免重命名和同名系统造成漂移。

### 2. 三张增量表保存可信范围

- `scope_containers`：租户、知识库、容器类型、系统标识和默认部门标记。
- `system_versions`：租户、系统、稳定版本键、显示名、状态和当前标记。
- `knowledge_scopes`：租户、文档、范围类型、系统、版本和可选项目。

三张表均保存 `tenant_id`，用于数据库唯一约束、组合外键和 fail-closed 查询。数据库保证每租户一个默认部门、每租户系统标识唯一、每系统一个当前版本，并限制版本引用删除。

选择独立表而不是给 `knowledges` 增加多个可空字段，是为了让旧知识库完全没有范围记录即可继续旧行为，也便于功能开关回滚和上游同步。

### 3. Scope Resolver 是唯一范围 seam

Resolver 只暴露 `ResolveWriteScope` 和 `ResolveQueryScope`。客户端提交知识库、版本和可选项目；Resolver 从知识库映射解析系统、校验版本归属、选择当前版本、自动加入默认部门并生成确切文档集合。

标签、明确文档和项目过滤在确切集合上取交集。任何范围缺失或不合法都失败，不回退为整个知识库。这样上传、聊天、Agent 和图谱不重复实现范围规则。

### 4. 文档与范围原子写入，异步任务只信任知识 ID

注册容器中的文档创建和 `knowledge_scopes` 插入使用同一数据库事务。Asynq 继续只携带 `knowledge_id`；Worker 从 SQL 重新加载范围，再执行解析、索引和图谱写入。

选择数据库重读而不是在任务载荷复制范围，是为了避免版本或项目参数被旧任务、重试或客户端载荷篡改。

### 5. A1 复用确切文档 ID 作为跨存储隔离合同

Resolver 将部门公共、系统公共和选定版本解析为每个 KB 的确切 `knowledge_ids`。现有向量/关键词检索继续复用 `SearchTargetTypeKnowledge`。Neo4j 节点/关系增加结构化属性，范围查询同时携带租户和确切文档 ID。

该方案避免一次性重写全部向量存储 Adapter；代价是大范围查询可能产生较长文档 ID 集合，后续如出现性能证据再为各存储增加原生结构化过滤。

### 6. Agent 工具在注册时绑定范围

结构化请求创建 Agent 工具时绑定冻结的 `ResolvedQueryScope`/`SearchTargets`。模型只能提供查询内容和不扩张范围的搜索控制，不能提交系统或版本。旧知识库继续使用现有构造器。

### 7. 功能开关和存量数据采用显式迁移

`ENABLE_KNOWLEDGE_SCOPE` 默认关闭。关闭时或知识库未登记为容器时走旧路径；登记容器且开关开启后执行严格校验。存量文档通过 preview/apply 两步显式赋值，标签只作为提示，不自动写入。

## Risks / Trade-offs

- [确切文档 ID 集合过大] → A1 先复用现有检索接口并记录规模/延迟；只有数据证明需要时才增加存储特定过滤。
- [部分链路遗漏范围] → 所有结构化入口只接受 Resolver 输出，并用跨 RAG/Agent/Neo4j 的同一夹具做隔离测试。
- [当前版本并发切换] → PostgreSQL 事务锁配合部分唯一索引；SQLite 单写者配合相同唯一约束。
- [注册容器后旧客户端上传缺少范围] → 返回明确 400，不自动归类为系统公共；前端与 API 文档同步发布。
- [Neo4j 旧节点缺少属性] → 存量迁移后重新抽取图谱；结构化查询绝不回退到旧节点。
- [上游合并冲突] → 新增独立 Scope 模块，只在上传、目标构建、工具注册和图谱接口各保留一个窄接入点。
- [单 change 较大] → 功能开关保持默认关闭，按核心、检索、图谱、UI 检查点提交并逐段验证。


```

Full source: openspec/changes/knowledge-scope-foundation/design.md

## openspec/changes/knowledge-scope-foundation/tasks.md

- Source: openspec/changes/knowledge-scope-foundation/tasks.md
- Lines: 1-108
- SHA256: c2e582b04f0791c38550861fa76975a08ec798b42fcfc775e9adf3f534d065c3

[TRUNCATED]

```md
## 1. Fork 与变更隔离

- [ ] 1.1 将 `origin` 指向 `zhujianye0759/WeKnora`、将腾讯仓库设为 `upstream`，并保留用户现有未提交文件
- [ ] 1.2 创建并切换到 `codex/knowledge-scope-foundation` 分支，确认本地 Git 用户名和邮箱
- [ ] 1.3 记录后续 `codex/sync-upstream-YYYYMMDD` 同步流程

## 2. 数据模型与功能开关

- [ ] 2.1 先编写范围类型合法组合和功能开关默认行为的失败测试
- [ ] 2.2 新增 `ScopeContainer`、`SystemVersion`、`KnowledgeScope`、选择值和校验方法
- [ ] 2.3 新增 PostgreSQL `000066_knowledge_scopes` 上下行迁移及租户级约束
- [ ] 2.4 新增 SQLite `000001_knowledge_scopes` 上下行迁移并验证现有 Lite 数据库可升级
- [ ] 2.5 增加默认关闭的 `ENABLE_KNOWLEDGE_SCOPE` 配置和示例说明
- [ ] 2.6 运行类型、配置和迁移测试并提交 schema 检查点

## 3. Scope Catalog

- [ ] 3.1 编写容器唯一性、当前版本并发、版本引用和租户隔离的失败测试
- [ ] 3.2 定义最小 `KnowledgeScopeRepository` 和 Catalog Service 接口
- [ ] 3.3 实现容器、版本和文档范围的租户限定 Repository
- [ ] 3.4 实现当前版本原子切换及被引用版本删除保护
- [ ] 3.5 实现文档与范围同事务创建和批量范围写入
- [ ] 3.6 在 Dig 容器注册 Repository/Service 并通过聚焦测试

## 4. 管理与存量迁移 API

- [ ] 4.1 编写范围容器、版本、迁移 preview/apply 的 Handler 和路由失败测试
- [ ] 4.2 实现范围容器和系统版本管理 Handler
- [ ] 4.3 实现显式迁移预览、逐行错误和确认后全事务应用
- [ ] 4.4 接入 Viewer/OwnedKBOrAdmin/KBAccess 及 API Key capability 守卫
- [ ] 4.5 验证功能关闭时新 API 返回稳定禁用错误且旧 API 不受影响

## 5. Scope Resolver

- [ ] 5.1 编写写入、当前/历史版本、多系统、部门公共、项目和旧 KB 的真值表测试
- [ ] 5.2 实现 `ResolveWriteScope` 并拒绝缺失或跨系统/租户版本
- [ ] 5.3 实现 `ResolveQueryScope`、当前版本回退和默认部门自动加入
- [ ] 5.4 将 `project_id` 解析为系统公共加精确项目版本文档集合
- [ ] 5.5 深拷贝 Resolver 输出并保证无效注册范围不进入 legacy fallback

## 6. 范围化写入与 Worker 继承

- [ ] 6.1 编写文件、URL、手工创建和事务回滚的失败测试
- [ ] 6.2 为三类创建接口增加末尾可选 `WriteScopeSelection`
- [ ] 6.3 在文件 multipart 和 URL/手工 JSON Handler 解析 `knowledge_scope`
- [ ] 6.4 将注册容器文档创建切换为原子 knowledge + scope 写入
- [ ] 6.5 处理重复内容与冲突范围，禁止静默改写已有范围
- [ ] 6.6 让 Worker/reparse 仅按 `knowledge_id` 从 SQL 重新读取可信范围
- [ ] 6.7 缺失范围时 fail closed，并拒绝现有 move 流程移动结构化文档

## 7. 范围化 RAG 与来源

- [ ] 7.1 编写 knowledge-chat、direct search、多系统和标签交集失败测试
- [ ] 7.2 给问答/搜索 DTO 和 `QARequest` 增加可选 `system_scopes`
- [ ] 7.3 在 `sessionService` 只解析一次范围并生成确切 `SearchTargets`
- [ ] 7.4 将明确文档和 OR 标签结果与允许文档集合取交集
- [ ] 7.5 在 `ChatManage.Clone` 深拷贝冻结的 `ResolvedQueryScope`
- [ ] 7.6 批量加载范围/版本信息并给检索结果增加来源字段
- [ ] 7.7 记录单系统失败，至少一个成功时发送 `scope_warning`，全部失败时返回错误

## 8. Neo4j 范围一致性

- [ ] 8.1 编写范围属性、参数化 Cypher、空范围和同名实体隔离测试
- [ ] 8.2 增加 `AddScopedGraph` 和 `SearchScopedNodes`，保留旧接口用于 legacy KB
- [ ] 8.3 在节点和关系写入租户、KB、文档、范围、系统、版本和项目属性
- [ ] 8.4 让注册文档图谱抽取使用数据库重读的可信范围
- [ ] 8.5 让实体检索使用租户加确切文档 ID，失败时不得回退标签查询

## 9. Agent 工具范围绑定

- [ ] 9.1 编写 Agent 伪造其他系统 KB/版本/文档参数的对抗测试
- [ ] 9.2 为结构化请求注册绑定冻结范围的 knowledge-search 工具 schema
- [ ] 9.3 为结构化请求注册真实调用 `SearchScopedNodes` 的 graph 工具
- [ ] 9.4 限制图谱返回后的 chunk/document 加载仍位于允许文档集合
- [ ] 9.5 保留旧知识库工具构造器并验证结构化与 legacy 两条路径

## 10. 知识库、版本、上传与迁移界面

- [ ] 10.1 新增集中式 `knowledgeScope.ts` 前端类型和 API 客户端
- [ ] 10.2 编写范围设置组件失败测试并实现容器/版本/当前版本管理

```

Full source: openspec/changes/knowledge-scope-foundation/tasks.md

## openspec/changes/knowledge-scope-foundation/specs/knowledge-scope-catalog/spec.md

- Source: openspec/changes/knowledge-scope-foundation/specs/knowledge-scope-catalog/spec.md
- Lines: 1-72
- SHA256: 9e01cbcc825c5b78eab5f187095df644cd0b117e50e52aeb35c13efdee07920c

```md
## ADDED Requirements

### Requirement: Tenant-scoped knowledge containers
系统 SHALL 允许将知识库登记为 `department_public` 或 `system` 容器，并 MUST 使用请求上下文中的租户而不是客户端提交的租户标识。

#### Scenario: Register a system container
- **WHEN** 有写权限的用户为知识库登记租户内唯一的 `system_id`
- **THEN** 系统保存系统容器映射，后续知识库改名不改变该系统标识

#### Scenario: Reject duplicate system identifier
- **WHEN** 同一租户的另一知识库尝试登记相同 `system_id`
- **THEN** 系统拒绝操作且保留原映射

#### Scenario: Separate tenants may reuse identifier
- **WHEN** 两个不同租户分别登记相同 `system_id`
- **THEN** 系统允许两条映射且查询互不访问

### Requirement: One default department knowledge base per tenant
系统 SHALL 允许每个租户指定且仅指定一个默认部门公共知识库。

#### Scenario: Set default department knowledge base
- **WHEN** 租户首次将部门公共容器设为默认
- **THEN** 系统保存默认标记并可供查询解析器读取

#### Scenario: Reject a second default
- **WHEN** 同一租户在未切换原默认项的情况下设置第二个默认部门容器
- **THEN** 数据库和服务共同保证最终最多只有一个默认项

### Requirement: Business system version lifecycle
系统 SHALL 为系统容器管理租户内归属明确的业务版本，并 MUST 保证同一系统内版本键唯一、最多一个当前版本。

#### Scenario: Create and select current version
- **WHEN** 用户创建版本并将其设为当前版本
- **THEN** 该系统的其他版本取消当前标记且目标版本成为唯一当前版本

#### Scenario: Concurrent current version updates
- **WHEN** 两个请求并发设置同一系统的不同当前版本
- **THEN** 事务和唯一约束保证提交完成后最多一个版本为当前

#### Scenario: Version status does not freeze writes
- **WHEN** 版本状态为 `active`、`maintenance` 或 `retired`
- **THEN** 状态仅用于展示且不禁止继续向该版本上传文档

#### Scenario: Referenced version cannot be deleted
- **WHEN** 版本已被任一文档范围引用且用户请求物理删除
- **THEN** 系统拒绝删除并允许修改其状态

### Requirement: Explicit legacy scope migration
系统 SHALL 提供存量文档范围的预览与确认应用流程，且 MUST NOT 根据标签自动提交结构化范围。

#### Scenario: Preview assignments
- **WHEN** 用户提交文档范围分配清单用于预览
- **THEN** 系统返回规范化结果和逐行错误且不写数据库

#### Scenario: Apply confirmed assignments atomically
- **WHEN** 用户确认一组全部有效的分配
- **THEN** 系统在一个事务中写入全部范围，任一行失败则全部回滚

#### Scenario: Tags are hints only
- **WHEN** 文档带有看似版本或项目的标签但用户未明确确认分配
- **THEN** 系统不创建 `knowledge_scopes` 记录

### Requirement: Feature flag and legacy catalog compatibility
系统 MUST 默认关闭结构化范围功能，并 SHALL 保留未登记知识库的原有行为。

#### Scenario: Feature disabled
- **WHEN** `ENABLE_KNOWLEDGE_SCOPE` 未设置或为 false
- **THEN** 新范围行为不影响现有知识库、上传和查询路径

#### Scenario: Unregistered knowledge base
- **WHEN** 功能已开启但目标知识库没有容器记录
- **THEN** 系统将其作为旧知识库处理且不推断系统或版本

```

## openspec/changes/knowledge-scope-foundation/specs/scoped-knowledge-ingestion/spec.md

- Source: openspec/changes/knowledge-scope-foundation/specs/scoped-knowledge-ingestion/spec.md
- Lines: 1-61
- SHA256: c353eeee2842e18a23da7aa46dbce92034654d403bbd263a0d543aea528167d2

```md
## ADDED Requirements

### Requirement: Validated write scope
系统 MUST 在注册容器的文件、URL 和手工知识创建前解析并校验写入范围。

#### Scenario: Department public upload
- **WHEN** 文档上传到部门公共容器
- **THEN** 系统将其范围固定为 `department_public` 且系统、版本、项目为空

#### Scenario: System common upload
- **WHEN** 用户在系统容器选择 `system_common`
- **THEN** 系统从容器解析可信 `system_id` 且版本、项目为空

#### Scenario: Version-specific upload
- **WHEN** 用户选择属于该系统的版本并可选填写 `project_id`
- **THEN** 系统保存 `system_version`、可信系统、版本和规范化项目值

#### Scenario: Reject missing or foreign version
- **WHEN** 注册系统知识库缺少合法范围或提交的版本属于其他系统/租户
- **THEN** 系统在创建文档前拒绝请求

### Requirement: Atomic document and scope persistence
系统 MUST 在同一个数据库事务中创建注册容器的知识文档和唯一结构化范围。

#### Scenario: Successful atomic create
- **WHEN** 文档与范围均通过校验并成功写入
- **THEN** 数据库同时存在 `knowledges` 和对应 `knowledge_scopes` 记录

#### Scenario: Scope insert failure
- **WHEN** 文档插入后范围插入违反约束或失败
- **THEN** 事务回滚且数据库不保留该文档

#### Scenario: Duplicate content with conflicting scope
- **WHEN** 同一知识库已存在相同内容但请求选择不同结构化范围
- **THEN** 系统返回冲突且不静默修改原文档范围

### Requirement: Worker scope inheritance
异步处理 Worker MUST 仅以 `knowledge_id` 定位文档，并从数据库重新加载可信结构化范围。

#### Scenario: Process scoped document
- **WHEN** Worker 开始解析注册容器中的文档
- **THEN** Worker 读取范围并将其用于后续索引和图谱写入

#### Scenario: Missing scope fails closed
- **WHEN** 注册容器文档没有可读取的合法范围
- **THEN** Worker 标记处理失败且不生成无范围索引或图谱数据

#### Scenario: Retry cannot change scope
- **WHEN** 同一任务被重试
- **THEN** Worker 再次从数据库读取范围而不信任旧任务载荷中的系统或版本参数

### Requirement: Processed scope immutability in A1
系统 MUST 拒绝通过现有文档移动功能改变结构化文档的系统或版本范围。

#### Scenario: Attempt to move a scoped document
- **WHEN** 用户请求将已结构化范围的文档移动到其他知识库或版本
- **THEN** 系统拒绝操作并提示删除后重新上传

#### Scenario: Reparse preserves scope
- **WHEN** 用户重新解析已结构化范围的文档
- **THEN** 系统保留原范围并由 Worker 重新读取使用

```

## openspec/changes/knowledge-scope-foundation/specs/scoped-knowledge-retrieval/spec.md

- Source: openspec/changes/knowledge-scope-foundation/specs/scoped-knowledge-retrieval/spec.md
- Lines: 1-99
- SHA256: c3ad67407e9d310f42fbf46ef980c22ef6a9e2aa9a7996cc916d683f4bc6e355

[TRUNCATED]

```md
## ADDED Requirements

### Requirement: Server-resolved system version query scope
系统 MUST 根据客户端选中的知识库和可选版本生成后端可信的查询范围，且模型或 Agent MUST NOT 扩大该范围。

#### Scenario: Default current version
- **WHEN** 用户选择已登记系统知识库但未显式指定版本
- **THEN** 系统使用该系统唯一当前版本

#### Scenario: Explicit historical version
- **WHEN** 用户为系统选择属于该系统的历史版本
- **THEN** 系统使用该历史版本而不改动当前版本标记

#### Scenario: Independent multi-system versions
- **WHEN** 用户同时选择系统 A/v1 和系统 B/v3
- **THEN** 系统分别解析两个范围并保留各自来源

#### Scenario: Reject forged version
- **WHEN** 请求为系统 A 提交系统 B 或其他租户的版本 ID
- **THEN** 系统在检索前拒绝请求且不执行降级查询

### Requirement: Required knowledge composition
系统查询 SHALL 自动包含租户默认部门公共知识、目标系统公共知识和目标系统选定版本知识。

#### Scenario: Resolve one system version
- **WHEN** 用户查询系统 A/v2
- **THEN** 允许集合仅包含默认部门公共、A/system-common 和 A/v2 文档

#### Scenario: Missing default department
- **WHEN** 已登记系统查询的租户未配置默认部门公共知识库
- **THEN** 系统返回配置错误且不静默忽略部门知识

#### Scenario: Missing current version
- **WHEN** 系统没有当前版本且请求未显式选择版本
- **THEN** 系统返回范围无法解析错误并提示选择版本

### Requirement: Auxiliary filters can only shrink scope
`project_id`、标签和明确文档选择 MUST 仅与结构化允许集合取交集，MUST NOT 增加其他系统或版本文档。

#### Scenario: Project filter
- **WHEN** 用户在 A/v2 选择项目 P100
- **THEN** 结果包含 A/system-common 和 A/v2/P100 文档，不包含 A/v2 的其他项目文档

#### Scenario: Tag OR cannot widen scope
- **WHEN** 所选标签同时匹配允许范围和其他系统/版本文档
- **THEN** 只保留结构化允许集合内的匹配文档

#### Scenario: Mentioned file outside scope
- **WHEN** 用户明确提及不属于所选系统版本的文档
- **THEN** 系统拒绝请求而不是扩大版本范围

### Requirement: Shared scope across retrieval paths
RAG、直接检索、Agent 知识搜索、Agent 图谱查询和 Neo4j 实体检索 MUST 使用同一次解析得到的范围或其不可扩张副本。

#### Scenario: RAG and Agent parity
- **WHEN** 同一 A/v2 请求分别执行普通 RAG 和 Agent 知识搜索
- **THEN** 两条路径访问的最大文档集合相同

#### Scenario: Scoped Neo4j query
- **WHEN** A 和 B 图谱存在同名实体且用户查询 A/v2
- **THEN** Neo4j 查询只返回允许文档 ID 关联的 A/v2、A/common 或部门节点

#### Scenario: Agent attempts expansion
- **WHEN** 模型向工具传入范围外的知识库、版本或文档参数
- **THEN** 工具忽略或拒绝这些参数且 Repository 不接收扩大的范围

#### Scenario: Graph failure does not fall open
- **WHEN** Neo4j 无法执行结构化范围查询
- **THEN** 图谱路径返回错误或不完整告警且不执行旧的全知识库查询

### Requirement: Scope provenance and partial failure reporting
系统 SHALL 为结构化检索结果返回范围来源，并 MUST 明确报告多系统查询中的部分失败。

#### Scenario: Version result provenance
- **WHEN** 返回系统版本文档结果
- **THEN** 结果包含范围类型、系统、版本、可选项目和原始文档来源

#### Scenario: Legacy result provenance
- **WHEN** 返回未登记旧知识库结果
- **THEN** 结果不伪造系统或版本范围字段

```

Full source: openspec/changes/knowledge-scope-foundation/specs/scoped-knowledge-retrieval/spec.md
