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
