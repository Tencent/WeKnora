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

#### Scenario: One selected system fails
- **WHEN** 多系统普通问答中至少一个系统检索成功且另一个失败
- **THEN** 系统返回成功证据并发送明确的不完整结果告警

#### Scenario: All selected systems fail
- **WHEN** 所有选中系统均检索失败
- **THEN** 系统返回检索错误且不生成看似完整的回答

### Requirement: Legacy query compatibility
功能关闭或知识库未登记时，系统 SHALL 保留现有知识库查询路径。

#### Scenario: Feature disabled query
- **WHEN** `ENABLE_KNOWLEDGE_SCOPE=false`
- **THEN** 原有知识库、文档和标签请求继续按现有行为执行

#### Scenario: Mixed registered and legacy query
- **WHEN** 功能开启且请求同时包含已登记系统 KB 和未登记旧 KB
- **THEN** 系统对已登记 KB 强制结构化范围，同时对旧 KB 使用现有目标构建逻辑
