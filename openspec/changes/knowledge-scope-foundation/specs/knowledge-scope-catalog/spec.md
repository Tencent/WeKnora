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
