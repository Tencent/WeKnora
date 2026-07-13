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
