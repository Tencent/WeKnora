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
- [ ] 10.3 将范围设置作为独立 section 接入现有知识库编辑器
- [ ] 10.4 编写上传范围选择失败测试并实现部门固定、系统公共、版本专属和可选项目
- [ ] 10.5 将同一 `knowledge_scope` 传给文件、URL 和手工上传
- [ ] 10.6 实现存量文档迁移 drawer、preview 和明确确认 apply
- [ ] 10.7 补齐中英韩俄文案并通过组件测试和类型检查

## 11. 问答版本选择与引用展示

- [ ] 11.1 编写每系统独立版本选择、持久化、移除清理和请求 payload 测试
- [ ] 11.2 在 settings store 增加按 KB ID 归一化的 `selectedSystemScopes`
- [ ] 11.3 在现有多知识库选择体验中增加 `SystemScopePicker`
- [ ] 11.4 将 `system_scopes` 发送给普通问答和 Agent 问答流接口
- [ ] 11.5 扩展 `referenceSources.ts` 和 `ChatReferencesDrawer.vue` 显示系统/版本/项目
- [ ] 11.6 在聊天界面显示部分系统失败的 `scope_warning`
- [ ] 11.7 通过前端测试、类型检查和生产构建

## 12. 隔离验证、文档和发布门禁

- [ ] 12.1 建立部门、A/common、A/v1、A/v2、多项目、B/common、B/v3 和 legacy 夹具
- [ ] 12.2 用同一允许 ID 集验证 RAG、direct search、Agent search、Agent graph 和 entity graph
- [ ] 12.3 覆盖跨租户、跨系统版本、标签扩张、越界文档、并发当前版本和缺失范围
- [ ] 12.4 覆盖 Neo4j/向量部分失败、全部失败、功能关闭和未登记兼容
- [ ] 12.5 运行完整 Go 测试、前端测试、type-check/build 和 `git diff --check`
- [ ] 12.6 使用 PostgreSQL + Neo4j Docker Compose 验证首页、健康检查和 A/B 零串数
- [ ] 12.7 编写 `docs/KnowledgeScope.md`、README 配置和回滚/上游同步说明
- [ ] 12.8 请求代码审查并在完成前重新运行全部验证

详细的逐文件 TDD 步骤、命令和提交边界见 `docs/superpowers/plans/2026-07-14-knowledge-scope-foundation.md`。
